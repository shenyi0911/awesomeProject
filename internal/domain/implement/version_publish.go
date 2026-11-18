package implement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/manifest"
	"awesomeProject/pkg/logger"
)

// Publish transitions a version from draft to released.
// Only one version per algorithm can be released; other non-archived versions are archived in the same transaction.
func (s *versionService) Publish(ctx context.Context, id string, operator string) (*entity.AlgorithmVersion, error) {
	v, err := s.verRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.Status == enum.VersionStatusReleased {
		return nil, errs.New(errs.KindConflict, "version already published")
	}
	if v.Status == enum.VersionStatusArchived {
		return nil, errs.New(errs.KindConflict, "archived version cannot be published")
	}

	// Pre-publish check: the version must contain the minimum file set required to launch
	// (algorithm.yaml and the main file referenced by entry).
	mfst, err := s.validateLaunchable(ctx, v)
	if err != nil {
		return nil, err
	}
	// Serialize the methods contract from algorithm.yaml into parameter_spec for gateway validation and the debug page.
	paramSpec := v.ParameterSpec
	if mfst != nil && len(mfst.Methods) > 0 {
		if b, jerr := json.Marshal(mfst.Methods); jerr == nil {
			paramSpec = string(b)
		} else {
			logger.Warn("publish: serialize methods contract failed; skipping persistence", "error", jerr)
		}
	}

	// Fetch the existing versions before the transaction changes their status, for audit logging.
	all, err := s.verRepo.ListAllByAlgorithm(ctx, v.AlgorithmID)
	if err != nil {
		return nil, err
	}
	// Atomic transition: archive other versions and mark this one released and active.
	if err := s.verRepo.ActivateVersion(ctx, v.AlgorithmID, v.ID, paramSpec); err != nil {
		return nil, err
	}

	for i := range all {
		old := &all[i]
		if old.ID == v.ID || old.Status == enum.VersionStatusArchived {
			continue
		}
		s.logChange(ctx, old, normalizeOperator(operator), enum.ChangeActionArchive, "automatically archived the previous version when publishing a new one", "")
	}
	v.Status = enum.VersionStatusReleased
	v.IsActive = true
	v.ParameterSpec = paramSpec
	v.UpdatedAt = time.Now()
	s.logChange(ctx, v, normalizeOperator(operator), enum.ChangeActionPublish, "publish version", "")
	return v, nil
}

// RevertPublish rolls a version whose publish failed back to draft and restores the previously released version.
// It compensates for a version that is published in the database but fails to launch, avoiding zombie versions.
func (s *versionService) RevertPublish(ctx context.Context, versionID, restoreVersionID, operator string) error {
	v, err := s.verRepo.GetByID(ctx, versionID)
	if err != nil {
		return err
	}
	if v.Status != enum.VersionStatusReleased {
		return nil
	}
	if err := s.verRepo.RollbackPublish(ctx, v.AlgorithmID, v.ID, restoreVersionID); err != nil {
		return err
	}
	v.Status = enum.VersionStatusDraft
	v.IsActive = false
	v.UpdatedAt = time.Now()
	desc := "publish failed, rolled back to draft"
	if restoreVersionID != "" {
		desc += ", previous released version restored"
	}
	s.logChange(ctx, v, normalizeOperator(operator), enum.ChangeActionPublish, desc, "")
	return nil
}

// validateLaunchable checks that the version has the minimum file set required for the runtime to launch it.
func (s *versionService) validateLaunchable(ctx context.Context, v *entity.AlgorithmVersion) (*manifest.Manifest, error) {
	if len(v.Files) == 0 {
		return nil, errs.New(errs.KindBadRequest, "version has no files; cannot launch (upload algorithm.yaml and runnable artifacts first)")
	}
	var yamlFile *entity.AlgorithmFile
	for i := range v.Files {
		if strings.EqualFold(filepath.Base(v.Files[i].Name), "algorithm.yaml") {
			yamlFile = &v.Files[i]
			break
		}
	}
	if yamlFile == nil {
		return nil, errs.New(errs.KindBadRequest, "missing algorithm.yaml (launch contract); cannot launch")
	}
	rc, derr := s.storage.Download(ctx, yamlFile.Path)
	if derr != nil {
		return nil, errs.Wrap(errs.KindInternal, derr, "read algorithm.yaml failed")
	}
	defer rc.Close()
	data, rerr := io.ReadAll(rc)
	if rerr != nil {
		return nil, errs.Wrap(errs.KindInternal, rerr, "read algorithm.yaml failed")
	}
	mfst, perr := validateManifestContent(data)
	if perr != nil {
		return nil, perr
	}
	mainFile := entryMainFile(mfst.Entry)
	if mainFile == "" {
		return nil, errs.New(errs.KindBadRequest, "cannot resolve the launch file from algorithm.yaml entry")
	}
	if !entryFilePresent(mainFile, v.Files) {
		return nil, errs.New(errs.KindBadRequest, fmt.Sprintf("launch file %q referenced by entry was not uploaded with this version; cannot launch", mainFile))
	}
	return mfst, nil
}

// entryMainFile extracts the executable name from the entry command, e.g. "dotnet SvyCalc.dll" -> "SvyCalc.dll".
func entryMainFile(entry string) string {
	fields := strings.Fields(entry)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// entryFilePresent reports whether the entry target exists in the version files (matched by relative path or file name).
func entryFilePresent(target string, files []entity.AlgorithmFile) bool {
	base := filepath.Base(target)
	for _, f := range files {
		if f.Name == target || filepath.Base(f.Name) == base {
			return true
		}
	}
	return false
}

// validateManifestContent parses and validates algorithm.yaml, returning an error when it cannot be parsed
// or lacks an entry.
func validateManifestContent(data []byte) (*manifest.Manifest, error) {
	mfst, err := manifest.Parse(data)
	if err != nil && !errors.Is(err, manifest.ErrMissingCode) {
		return nil, errs.Wrap(errs.KindBadRequest, err, "parse algorithm.yaml failed")
	}
	if mfst == nil || mfst.Entry == "" {
		return nil, errs.New(errs.KindBadRequest, "algorithm.yaml is missing entry (launch command) and cannot serve as the launch contract")
	}
	if !strings.HasPrefix(mfst.Health, "/") {
		return nil, errs.New(errs.KindBadRequest, "algorithm.yaml health must be a path starting with '/'")
	}
	return mfst, nil
}

// Archive transitions released/draft to archived (logical delete): history is kept but new invocations are rejected.
func (s *versionService) Archive(ctx context.Context, id string, operator string) (*entity.AlgorithmVersion, error) {
	v, err := s.verRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.Status == enum.VersionStatusArchived {
		return nil, errs.New(errs.KindConflict, "version already archived")
	}
	if err := s.verRepo.ArchiveVersion(ctx, v.ID); err != nil {
		return nil, err
	}
	v.Status = enum.VersionStatusArchived
	v.IsActive = false
	v.UpdatedAt = time.Now()
	s.logChange(ctx, v, normalizeOperator(operator), enum.ChangeActionArchive, "archive version", "")
	return v, nil
}
