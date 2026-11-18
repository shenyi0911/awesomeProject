package implement

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/port"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

type versionService struct {
	algoRepo port.AlgorithmRepository
	verRepo  port.VersionRepository
	storage  port.FileStorage
}

// NewVersionService creates the version service.
func NewVersionService(algoRepo port.AlgorithmRepository, verRepo port.VersionRepository, storage port.FileStorage) *versionService {
	return &versionService{algoRepo: algoRepo, verRepo: verRepo, storage: storage}
}

var versionNumRe = regexp.MustCompile(`(\d+)`)

// versionPattern restricts version strings to characters that are safe in paths and object keys.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// maxVersionLength matches the algorithm_versions.version column.
const maxVersionLength = 64

// validateVersion rejects versions that contain numbers but are unsafe as path segments.
func validateVersion(ver string) error {
	if len(ver) > maxVersionLength {
		return errs.New(errs.KindBadRequest, "version must be at most 64 characters")
	}
	if !versionPattern.MatchString(ver) {
		return errs.New(errs.KindBadRequest, "version may contain only letters, numbers, dots, dashes and underscores, and must start with a letter or number")
	}
	if !versionNumRe.MatchString(ver) {
		return errs.New(errs.KindBadRequest, "version must contain a number, e.g. v1, v1.0.0")
	}
	return nil
}

func (s *versionService) Create(ctx context.Context, req request.CreateVersionRequest) (*entity.AlgorithmVersion, error) {
	algoID := strings.TrimSpace(req.AlgorithmID)
	if _, err := s.algoRepo.GetByID(ctx, algoID); err != nil {
		return nil, err
	}

	versions, err := s.verRepo.ListAllByAlgorithm(ctx, algoID)
	if err != nil {
		return nil, err
	}
	// A released version may coexist with a new draft, enabling upgrades without archiving first.
	// Only one draft at a time is allowed, so publishing/archiving is the only way forward.
	for _, v := range versions {
		if v.Status == enum.VersionStatusDraft {
			return nil, errs.New(errs.KindConflict, "please publish or archive the existing draft before creating a new version")
		}
	}

	ver := strings.TrimSpace(req.Version)
	if ver == "" {
		ver = nextVersionNumber(versions)
	}
	if err := validateVersion(ver); err != nil {
		return nil, err
	}

	newVer := &entity.AlgorithmVersion{
		ID:            utils.NewID("ver"),
		AlgorithmID:   algoID,
		Version:       ver,
		Description:   strings.TrimSpace(req.Description),
		Status:        enum.VersionStatusDraft,
		IsActive:      false,
		EntryPoint:    strings.TrimSpace(req.EntryPoint),
		ParameterSpec: strings.TrimSpace(req.ParameterSpec),
		ReleaseNote:   strings.TrimSpace(req.ReleaseNote),
		Files:         []entity.AlgorithmFile{},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := s.verRepo.Create(ctx, newVer); err != nil {
		logger.Error("create version failed", "error", err)
		return nil, err
	}
	s.logChange(ctx, newVer, normalizeOperator(req.Operator), enum.ChangeActionCreate, "create version", "")
	return newVer, nil
}

// nextVersionNumber generates the next version per algorithm (max major version + 1).
func nextVersionNumber(versions []entity.AlgorithmVersion) string {
	maxN := 0
	for _, v := range versions {
		if n := parseMajorVersion(v.Version); n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("v%d", maxN+1)
}

// parseMajorVersion parses the major version number, e.g. v1.0.0 -> 1, returning 0 when invalid.
func parseMajorVersion(v string) int {
	parts := strings.Split(v, ".")
	if len(parts) == 0 {
		return 0
	}
	nums := versionNumRe.FindStringSubmatch(parts[0])
	if len(nums) < 2 {
		return 0
	}
	n, err := strconv.Atoi(nums[1])
	if err != nil {
		return 0
	}
	return n
}

func (s *versionService) Update(ctx context.Context, id string, req request.UpdateVersionRequest) (*entity.AlgorithmVersion, error) {
	v, err := s.verRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Constraint: only drafts are editable; released/archived versions are immutable and require a new version.
	if v.Status != enum.VersionStatusDraft {
		return nil, errs.New(errs.KindConflict, "only draft version can be edited; archived/published versions are immutable")
	}

	if req.Description != "" {
		v.Description = strings.TrimSpace(req.Description)
	}
	if req.EntryPoint != "" {
		v.EntryPoint = strings.TrimSpace(req.EntryPoint)
	}
	if req.ParameterSpec != "" {
		v.ParameterSpec = strings.TrimSpace(req.ParameterSpec)
	}
	if req.ReleaseNote != "" {
		v.ReleaseNote = strings.TrimSpace(req.ReleaseNote)
	}

	if err := s.verRepo.UpdateMeta(ctx, v); err != nil {
		logger.Error("update version failed", "error", err)
		return nil, err
	}
	v.UpdatedAt = time.Now()
	s.logChange(ctx, v, normalizeOperator(req.Operator), enum.ChangeActionEdit, "edit version metadata", "")
	return v, nil
}

func (s *versionService) Delete(ctx context.Context, id string) error {
	// Delete change logs first, then the version record (files are removed by the foreign key cascade).
	if err := s.verRepo.DeleteChangeLogs(ctx, id); err != nil {
		logger.Warn("delete version change logs failed", "id", id, "error", err)
	}
	return s.verRepo.Delete(ctx, id)
}

func (s *versionService) GetByID(ctx context.Context, id string) (*entity.AlgorithmVersion, error) {
	return s.verRepo.GetByID(ctx, id)
}

// AlgorithmCode returns the algorithm code for an algorithm ID, used by the runtime to locate the package and Dapr app ID.
func (s *versionService) AlgorithmCode(ctx context.Context, algoID string) (string, error) {
	algo, err := s.algoRepo.GetByID(ctx, algoID)
	if err != nil {
		return "", err
	}
	return algo.Code, nil
}

func (s *versionService) List(ctx context.Context, req request.ListVersionRequest) ([]entity.AlgorithmVersion, int, error) {
	limit, offset := utils.NormalizePaging(req.Limit, req.Offset)
	return s.verRepo.ListByAlgorithm(ctx, req.AlgorithmID, request.VersionFilter{
		Version: req.Version,
		Status:  req.Status,
		Limit:   limit,
		Offset:  offset,
	})
}

func (s *versionService) ListAll(ctx context.Context, algorithmID string) ([]entity.AlgorithmVersion, error) {
	return s.verRepo.ListAllByAlgorithm(ctx, algorithmID)
}

func (s *versionService) GetActive(ctx context.Context, algorithmID string) (*entity.AlgorithmVersion, error) {
	return s.verRepo.GetActiveByAlgorithm(ctx, algorithmID)
}
