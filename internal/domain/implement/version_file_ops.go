package implement

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

// fileURLExpiry is the lifetime of a presigned download URL.
const fileURLExpiry = 15 * time.Minute

// Column limits mirrored from schema.sql.
const (
	maxRelPathLength     = 255
	maxContentTypeLength = 128
	maxFileTypeLength    = 64
)

// validateUploadMeta validates fields that map to bounded database columns.
func validateUploadMeta(relPath, contentType string) error {
	if len(contentType) > maxContentTypeLength {
		return errs.New(errs.KindBadRequest, "content type is too long")
	}
	ext := strings.TrimPrefix(path.Ext(relPath), ".")
	if len(ext) > maxFileTypeLength {
		return errs.New(errs.KindBadRequest, "file extension is too long")
	}
	return nil
}

// normalizeUploadPath validates and normalizes an uploaded relative path: no absolute paths,
// no traversal segments, and only forward slashes as separators.
func normalizeUploadPath(relPath, filename string) (string, error) {
	p := strings.TrimSpace(relPath)
	if p == "" {
		p = strings.TrimSpace(filename)
	}
	if p == "" {
		return "", errs.New(errs.KindBadRequest, "file path is required")
	}
	if len(p) > maxRelPathLength {
		return "", errs.New(errs.KindBadRequest, "file path is too long")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return "", errs.New(errs.KindBadRequest, "absolute file path is not allowed")
	}
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s == "" || s == "." {
			continue
		}
		if s == ".." || strings.Contains(s, ":") || strings.ContainsRune(s, 0) {
			return "", errs.New(errs.KindBadRequest, fmt.Sprintf("invalid file path segment %q", s))
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return "", errs.New(errs.KindBadRequest, "file path is required")
	}
	return strings.Join(out, "/"), nil
}

// zipEntryName makes a stored file name safe as a zip entry, dropping traversal and absolute segments.
func zipEntryName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	segs := strings.Split(name, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || strings.Contains(s, ":") || strings.ContainsRune(s, 0) {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return "file"
	}
	return strings.Join(out, "/")
}

// UploadFile uploads a version file. Only drafts accept uploads. Uploading the same relative path
// again replaces the previous entry and removes the superseded object after persistence. Released
// and archived versions are immutable, so their history is never touched.
func (s *versionService) UploadFile(ctx context.Context, versionID string, in request.UploadFileInput, operator string) (*entity.AlgorithmFile, error) {
	v, err := s.verRepo.GetByID(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if v.Status != enum.VersionStatusDraft {
		return nil, errs.New(errs.KindConflict, "only draft version can upload files")
	}

	relPath, err := normalizeUploadPath(in.RelativePath, in.Filename)
	if err != nil {
		return nil, err
	}
	if err := validateUploadMeta(relPath, in.ContentType); err != nil {
		return nil, err
	}
	key := buildVersionFileKey(versionID, relPath)

	var (
		md5sum     string
		actualSize int64
	)
	if strings.EqualFold(filepath.Base(relPath), "algorithm.yaml") {
		data, rerr := io.ReadAll(in.Reader)
		if rerr != nil {
			return nil, errs.Wrap(errs.KindBadRequest, rerr, "read upload stream failed")
		}
		if _, verr := validateManifestContent(data); verr != nil {
			return nil, verr
		}
		md5sum = fmt.Sprintf("%x", md5.Sum(data))
		actualSize = int64(len(data))
		if err := s.storage.Upload(ctx, key, bytes.NewReader(data), actualSize, in.ContentType); err != nil {
			return nil, err
		}
	} else {
		uploadSize := in.Size
		if uploadSize <= 0 {
			uploadSize = -1 // Unknown size: let the S3 client stream the upload.
		}
		h := md5.New()
		cr := &countingReader{r: in.Reader}
		if err := s.storage.Upload(ctx, key, io.TeeReader(cr, h), uploadSize, in.ContentType); err != nil {
			return nil, err
		}
		md5sum = fmt.Sprintf("%x", h.Sum(nil))
		actualSize = cr.n
	}

	file := &entity.AlgorithmFile{
		Name:        relPath,
		Path:        key,
		Size:        actualSize,
		Type:        strings.TrimPrefix(path.Ext(relPath), "."),
		ContentType: in.ContentType,
	}
	files, replaced, err := s.mutateFiles(ctx, versionID, v.AlgorithmID, func(current []entity.AlgorithmFile) ([]entity.AlgorithmFile, []string, error) {
		out, superseded := replaceFile(current, *file)
		return out, superseded, nil
	})
	if err != nil {
		_ = s.storage.Delete(ctx, key)
		return nil, err
	}
	s.deleteSupersededFiles(ctx, files, replaced)
	s.logChange(ctx, v, normalizeOperator(operator), enum.ChangeActionUploadFile, "upload file: "+relPath, md5sum)
	return file, nil
}

// UploadFiles uploads multiple version files in one request, producing a single change log entry
// and rolling back uploaded objects if any file fails. Files with the same relative path replace
// earlier entries, including duplicates within the same request.
func (s *versionService) UploadFiles(ctx context.Context, versionID string, ins []request.UploadFileInput, operator string) ([]*entity.AlgorithmFile, error) {
	if len(ins) == 0 {
		return nil, errs.New(errs.KindBadRequest, "no files to upload")
	}
	v, err := s.verRepo.GetByID(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if v.Status != enum.VersionStatusDraft {
		return nil, errs.New(errs.KindConflict, "only draft version can upload files")
	}

	var (
		uploaded []string
		files    []*entity.AlgorithmFile
		names    []string
	)
	for _, in := range ins {
		relPath, perr := normalizeUploadPath(in.RelativePath, in.Filename)
		if perr != nil {
			s.rollbackUploads(ctx, uploaded)
			return nil, perr
		}
		if perr := validateUploadMeta(relPath, in.ContentType); perr != nil {
			s.rollbackUploads(ctx, uploaded)
			return nil, perr
		}
		key := buildVersionFileKey(versionID, relPath)

		var size int64
		if strings.EqualFold(filepath.Base(relPath), "algorithm.yaml") {
			data, rerr := io.ReadAll(in.Reader)
			if rerr != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, errs.Wrap(errs.KindBadRequest, rerr, "read upload stream failed")
			}
			if _, verr := validateManifestContent(data); verr != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, verr
			}
			if err := s.storage.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), in.ContentType); err != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, err
			}
			size = int64(len(data))
		} else {
			uploadSize := in.Size
			if uploadSize <= 0 {
				uploadSize = -1
			}
			cr := &countingReader{r: in.Reader}
			if err := s.storage.Upload(ctx, key, cr, uploadSize, in.ContentType); err != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, err
			}
			size = cr.n
		}
		uploaded = append(uploaded, key)
		files = append(files, &entity.AlgorithmFile{
			Name:        relPath,
			Path:        key,
			Size:        size,
			Type:        strings.TrimPrefix(path.Ext(relPath), "."),
			ContentType: in.ContentType,
		})
		names = append(names, relPath)
	}

	incoming := make([]entity.AlgorithmFile, 0, len(files))
	for _, f := range files {
		incoming = append(incoming, *f)
	}
	saved, replaced, err := s.mutateFiles(ctx, versionID, v.AlgorithmID, func(current []entity.AlgorithmFile) ([]entity.AlgorithmFile, []string, error) {
		var superseded []string
		for _, f := range incoming {
			var sup []string
			current, sup = replaceFile(current, f)
			superseded = append(superseded, sup...)
		}
		return current, superseded, nil
	})
	if err != nil {
		s.rollbackUploads(ctx, uploaded)
		return nil, err
	}
	s.deleteSupersededFiles(ctx, saved, replaced)
	s.logChange(ctx, v, normalizeOperator(operator), enum.ChangeActionUploadFile, batchUploadDesc(names), "")
	return files, nil
}

// rollbackUploads deletes objects uploaded in the current request after a failure.
func (s *versionService) rollbackUploads(ctx context.Context, keys []string) {
	for _, k := range keys {
		_ = s.storage.Delete(ctx, k)
	}
}

// maxFileSaveAttempts bounds optimistic-lock retries for file mutations.
const maxFileSaveAttempts = 3

// filesMutation applies a change to the current file set and returns the object keys that are no
// longer referenced.
type filesMutation func(files []entity.AlgorithmFile) ([]entity.AlgorithmFile, []string, error)

// mutateFiles re-reads the version, applies mutate and saves with optimistic locking, retrying when
// another request modified the version concurrently.
func (s *versionService) mutateFiles(ctx context.Context, versionID, algorithmID string, mutate filesMutation) ([]entity.AlgorithmFile, []string, error) {
	for attempt := 1; attempt <= maxFileSaveAttempts; attempt++ {
		v, err := s.verRepo.GetByID(ctx, versionID)
		if err != nil {
			return nil, nil, err
		}
		if v.Status != enum.VersionStatusDraft {
			return nil, nil, errs.New(errs.KindConflict, "only draft version can modify files")
		}
		files, removed, err := mutate(v.Files)
		if err != nil {
			return nil, nil, err
		}
		if err := s.verRepo.SaveFiles(ctx, versionID, algorithmID, files, v.Revision); err != nil {
			if errs.KindOf(err) == errs.KindConflict && attempt < maxFileSaveAttempts {
				continue
			}
			return nil, nil, err
		}
		return files, removed, nil
	}
	return nil, nil, errs.New(errs.KindConflict, "version files were modified concurrently; please retry")
}

// replaceFile inserts incoming, replacing any entry with the same relative path.
// It returns the superseded object keys so the caller can clean them up after persistence succeeds.
func replaceFile(files []entity.AlgorithmFile, incoming entity.AlgorithmFile) ([]entity.AlgorithmFile, []string) {
	out := make([]entity.AlgorithmFile, 0, len(files)+1)
	var superseded []string
	for _, f := range files {
		if f.Name == incoming.Name {
			superseded = append(superseded, f.Path)
			continue
		}
		out = append(out, f)
	}
	return append(out, incoming), superseded
}

// deleteSupersededFiles removes objects no longer referenced after an overwrite. Keys still present
// in files are kept, so the final version of a duplicated path is never deleted.
func (s *versionService) deleteSupersededFiles(ctx context.Context, files []entity.AlgorithmFile, keys []string) {
	inUse := make(map[string]bool, len(files))
	for _, f := range files {
		inUse[f.Path] = true
	}
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if k == "" || inUse[k] || seen[k] {
			continue
		}
		seen[k] = true
		if err := s.storage.Delete(ctx, k); err != nil {
			logger.Warn("delete superseded version file failed", "key", k, "error", err)
		}
	}
}

// batchUploadDesc builds a summary for batch uploads: lists all names when there are at most 5,
// otherwise the first 3 plus the total.
func batchUploadDesc(names []string) string {
	summary := fmt.Sprintf(" (%d files)", len(names))
	if len(names) <= 5 {
		return "upload files: " + strings.Join(names, ", ") + summary
	}
	return "upload files: " + strings.Join(names[:3], ", ") + " and more" + summary
}

// DeleteFile deletes a version file. Only drafts allow deletions. The database is updated first,
// then the object is removed best-effort so a storage failure cannot leave a dangling reference.
func (s *versionService) DeleteFile(ctx context.Context, versionID, key, operator string) error {
	v, err := s.verRepo.GetByID(ctx, versionID)
	if err != nil {
		return err
	}
	if v.Status != enum.VersionStatusDraft {
		return errs.New(errs.KindConflict, "only draft version can delete files")
	}
	var name string
	files, removed, err := s.mutateFiles(ctx, versionID, v.AlgorithmID, func(current []entity.AlgorithmFile) ([]entity.AlgorithmFile, []string, error) {
		idx := -1
		for i, f := range current {
			if f.Path == key || f.Name == key {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, nil, errs.NotFoundf("file not found in version: %s", key)
		}
		name = current[idx].Name
		actualKey := current[idx].Path
		return append(current[:idx], current[idx+1:]...), []string{actualKey}, nil
	})
	if err != nil {
		return err
	}
	s.deleteSupersededFiles(ctx, files, removed)
	s.logChange(ctx, v, normalizeOperator(operator), enum.ChangeActionDeleteFile, "delete file: "+name, "")
	return nil
}

// GetFileURL returns a download URL for a version file (read-only, no change log).
func (s *versionService) GetFileURL(ctx context.Context, versionID, key string) (string, error) {
	v, err := s.verRepo.GetByID(ctx, versionID)
	if err != nil {
		return "", err
	}
	for _, f := range v.Files {
		if f.Path == key || f.Name == key {
			return s.storage.PresignURL(ctx, f.Path, fileURLExpiry)
		}
	}
	return "", errs.NotFoundf("file not found in version: %s", key)
}

// fileSelection is a file selected for batch download (zip entry name and object key).
type fileSelection struct {
	zipName string
	key     string
}

// maxBatchDownloadKeys bounds one zip download request.
const maxBatchDownloadKeys = 500

// BatchDownloadFiles streams the selected files as a zip archive without loading everything into memory.
// keys may be full paths or relative names; missing files are skipped; zip entries keep their relative paths.
func (s *versionService) BatchDownloadFiles(ctx context.Context, versionID string, keys []string) (io.ReadCloser, string, error) {
	v, err := s.verRepo.GetByID(ctx, versionID)
	if err != nil {
		return nil, "", err
	}
	if len(keys) == 0 {
		return nil, "", errs.New(errs.KindBadRequest, "no file selected")
	}
	if len(keys) > maxBatchDownloadKeys {
		return nil, "", errs.New(errs.KindBadRequest, "too many files selected for one download")
	}

	seen := map[string]int{}
	selected := make([]fileSelection, 0, len(keys))
	for _, key := range keys {
		var actualKey, relName string
		for _, f := range v.Files {
			if f.Path == key || f.Name == key {
				actualKey, relName = f.Path, zipEntryName(f.Name)
				break
			}
		}
		if actualKey == "" {
			continue
		}
		entry := relName
		if n, ok := seen[entry]; ok {
			seen[entry] = n + 1
			entry = fmt.Sprintf("%s_%d", entry, n)
		} else {
			seen[entry] = 1
		}
		selected = append(selected, fileSelection{zipName: entry, key: actualKey})
	}
	if len(selected) == 0 {
		return nil, "", errs.NotFoundf("no matching files for version %s", versionID)
	}

	pr, pw := io.Pipe()
	go func() {
		zw := zip.NewWriter(pw)
		for _, sel := range selected {
			rc, derr := s.storage.Download(ctx, sel.key)
			if derr != nil {
				_ = zw.Close()
				_ = pw.CloseWithError(fmt.Errorf("download %s failed: %w", sel.zipName, derr))
				return
			}
			w, werr := zw.Create(sel.zipName)
			if werr != nil {
				rc.Close()
				_ = zw.Close()
				_ = pw.CloseWithError(werr)
				return
			}
			if _, cerr := io.Copy(w, rc); cerr != nil {
				rc.Close()
				_ = zw.Close()
				_ = pw.CloseWithError(cerr)
				return
			}
			rc.Close()
		}
		if cerr := zw.Close(); cerr != nil {
			_ = pw.CloseWithError(cerr)
			return
		}
		_ = pw.Close()
	}()

	return pr, fmt.Sprintf("version-%s-files.zip", v.Version), nil
}

// countingReader wraps an io.Reader and counts bytes read, recording the actual size during streaming uploads.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// buildVersionFileKey builds the object storage key for a version file: versions/{versionID}/{uuid}/{relativePath}.
func buildVersionFileKey(versionID, relativePath string) string {
	p := strings.ReplaceAll(relativePath, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	segs := strings.Split(p, "/")
	for i, st := range segs {
		if st == ".." || st == "" {
			segs[i] = "_"
		}
	}
	p = strings.Join(segs, "/")
	return fmt.Sprintf("versions/%s/%s/%s", versionID, utils.NewID("f"), p)
}
