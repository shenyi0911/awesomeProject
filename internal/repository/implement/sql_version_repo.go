package implement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/repository/mapper"
	"awesomeProject/pkg/utils"
)

// sqlVersionRepo is the PostgreSQL implementation of the version repository.
// SQL statements live in mapper/sql/version.sql (annotated with -- name: Xxx); this file only
// fetches, binds and scans.
type sqlVersionRepo struct {
	db *sql.DB
}

var (
	_ port.VersionRepository = (*sqlVersionRepo)(nil)
	_ port.VersionResolver   = (*sqlVersionRepo)(nil)
)

// NewSQLVersionRepository creates the SQL version repository.
func NewSQLVersionRepository(db *sql.DB) *sqlVersionRepo {
	return &sqlVersionRepo{db: db}
}

// scanVersion scans a version from one row (files are loaded separately by loadFiles).
func scanVersion(scan func(...any) error) (*entity.AlgorithmVersion, error) {
	v := &entity.AlgorithmVersion{}
	var status, desc, entryPoint, paramSpec, releaseNote sql.NullString
	if err := scan(&v.ID, &v.AlgorithmID, &v.Version, &desc, &status,
		&v.IsActive, &entryPoint, &paramSpec, &releaseNote, &v.CreatedAt, &v.UpdatedAt, &v.Revision); err != nil {
		return nil, err
	}
	v.Status = enum.VersionStatus(status.String)
	v.Description = desc.String
	v.EntryPoint = entryPoint.String
	v.ParameterSpec = paramSpec.String
	v.ReleaseNote = releaseNote.String
	return v, nil
}

func (r *sqlVersionRepo) loadFiles(ctx context.Context, versionID string) ([]entity.AlgorithmFile, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("VersionFileLoadByVersionID"), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []entity.AlgorithmFile{}
	for rows.Next() {
		var f entity.AlgorithmFile
		var fid, vid, aid, name, path, ftype, ctype sql.NullString
		var size sql.NullInt64
		if err := rows.Scan(&fid, &vid, &aid, &name, &path, &size, &ftype, &ctype); err != nil {
			return nil, err
		}
		f.Name = name.String
		f.Path = path.String
		f.Size = size.Int64
		f.Type = ftype.String
		f.ContentType = ctype.String
		files = append(files, f)
	}
	return files, rows.Err()
}

// loadFilesBatch loads files for multiple versions at once and groups them by version,
// avoiding N+1 queries in list scenarios.
func (r *sqlVersionRepo) loadFilesBatch(ctx context.Context, versionIDs []string) (map[string][]entity.AlgorithmFile, error) {
	out := map[string][]entity.AlgorithmFile{}
	if len(versionIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, mapper.SQL("VersionFilesByVersionIDs"), pq.Array(versionIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var vid, name, path, ftype, ctype sql.NullString
		var size sql.NullInt64
		if err := rows.Scan(&vid, &name, &path, &size, &ftype, &ctype); err != nil {
			return nil, err
		}
		out[vid.String] = append(out[vid.String], entity.AlgorithmFile{
			Name:        name.String,
			Path:        path.String,
			Size:        size.Int64,
			Type:        ftype.String,
			ContentType: ctype.String,
		})
	}
	return out, rows.Err()
}

// saveFilesTx clears the version files and rewrites the current Files slice in one transaction
// (idempotent, keeping the table consistent with the in-memory object).
func (r *sqlVersionRepo) saveFilesTx(ctx context.Context, tx *sql.Tx, versionID, algorithmID string, files []entity.AlgorithmFile) error {
	if _, err := tx.ExecContext(ctx, mapper.SQL("VersionFileDeleteByVersionID"), versionID); err != nil {
		return fmt.Errorf("delete old files failed: %w", err)
	}
	for i, f := range files {
		if _, err := tx.ExecContext(ctx, mapper.SQL("VersionFileInsert"),
			utils.NewID("f"), versionID, algorithmID, f.Name, f.Path, f.Size, f.Type, f.ContentType, time.Now(), i,
		); err != nil {
			return fmt.Errorf("insert file %s failed: %w", f.Name, err)
		}
	}
	return nil
}

func (r *sqlVersionRepo) Create(ctx context.Context, v *entity.AlgorithmVersion) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, mapper.SQL("VersionCreate"),
		v.ID, v.AlgorithmID, v.Version, v.Description, string(v.Status),
		v.IsActive, v.EntryPoint, v.ParameterSpec, v.ReleaseNote, v.CreatedAt, v.UpdatedAt, v.Revision,
	); err != nil {
		if isUniqueViolation(err) {
			return errs.New(errs.KindConflict, "version already exists: "+v.Version)
		}
		return err
	}
	if err := r.saveFilesTx(ctx, tx, v.ID, v.AlgorithmID, v.Files); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateMeta updates version metadata only, leaving status and files untouched
// (avoiding a full file rewrite during status transitions).
func (r *sqlVersionRepo) UpdateMeta(ctx context.Context, v *entity.AlgorithmVersion) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("VersionUpdateMeta"),
		v.ID, v.Description, v.EntryPoint, v.ParameterSpec, v.ReleaseNote, time.Now())
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		// The status guard also covers a concurrent publish between the service check and this update.
		return errs.New(errs.KindConflict, "only draft version can be edited; archived/published versions are immutable")
	}
	return nil
}

// SaveFiles overwrites version files with the current set (called after uploading or deleting files).
// The revision check turns concurrent modifications into a conflict instead of silently losing updates.
func (r *sqlVersionRepo) SaveFiles(ctx context.Context, versionID, algorithmID string, files []entity.AlgorithmFile, expectedRevision int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, mapper.SQL("VersionBumpRevision"), versionID, time.Now(), expectedRevision)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errs.New(errs.KindConflict, "version files were modified concurrently")
	}
	if err := r.saveFilesTx(ctx, tx, versionID, algorithmID, files); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sqlVersionRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("VersionDelete"), id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errs.NotFoundf("version not found: %s", id)
	}
	return nil
}

func (r *sqlVersionRepo) GetByID(ctx context.Context, id string) (*entity.AlgorithmVersion, error) {
	row := r.db.QueryRowContext(ctx, mapper.SQL("VersionGetByID"), id)
	v, err := scanVersion(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errs.NotFoundf("version not found: %s", id)
		}
		return nil, err
	}
	v.Files, err = r.loadFiles(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (r *sqlVersionRepo) ListByAlgorithm(ctx context.Context, algorithmID string, filter request.VersionFilter) ([]entity.AlgorithmVersion, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, mapper.SQL("VersionCountByAlgorithm"),
		algorithmID, filter.Version, string(filter.Status)).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, mapper.SQL("VersionListByAlgorithm"),
		algorithmID, filter.Version, string(filter.Status), filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	result := []entity.AlgorithmVersion{}
	ids := make([]string, 0)
	for rows.Next() {
		v, err := scanVersion(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, *v)
		ids = append(ids, v.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	files, err := r.loadFilesBatch(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range result {
		result[i].Files = files[result[i].ID]
		if result[i].Files == nil {
			result[i].Files = []entity.AlgorithmFile{}
		}
	}
	return result, total, nil
}

// ListAllByAlgorithm returns all versions of an algorithm (used by internal rules and stats).
func (r *sqlVersionRepo) ListAllByAlgorithm(ctx context.Context, algorithmID string) ([]entity.AlgorithmVersion, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("VersionListAllByAlgorithm"), algorithmID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []entity.AlgorithmVersion{}
	ids := make([]string, 0)
	for rows.Next() {
		v, err := scanVersion(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, *v)
		ids = append(ids, v.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	files, err := r.loadFilesBatch(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].Files = files[result[i].ID]
		if result[i].Files == nil {
			result[i].Files = []entity.AlgorithmFile{}
		}
	}
	return result, nil
}

func (r *sqlVersionRepo) GetActiveByAlgorithm(ctx context.Context, algorithmID string) (*entity.AlgorithmVersion, error) {
	row := r.db.QueryRowContext(ctx, mapper.SQL("VersionGetActiveByAlgorithm"), algorithmID)
	v, err := scanVersion(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errs.NotFoundf("no active version for algorithm: %s", algorithmID)
		}
		return nil, err
	}
	v.Files, err = r.loadFiles(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// ActivateVersion archives the algorithm's other versions and marks the target released and active
// in one transaction. SELECT ... FOR UPDATE locks the algorithm row, serializing concurrent publishes
// so only one released version can exist.
func (r *sqlVersionRepo) ActivateVersion(ctx context.Context, algorithmID, versionID, parameterSpec string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lockedID string
	if err := tx.QueryRowContext(ctx, mapper.SQL("VersionLockAlgorithm"), algorithmID).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errs.NotFoundf("algorithm not found: %s", algorithmID)
		}
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, mapper.SQL("VersionArchiveOthers"), algorithmID, now, versionID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, mapper.SQL("VersionActivate"), versionID, parameterSpec, now, algorithmID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errs.NotFoundf("version not belong to algorithm: %s", versionID)
	}
	return tx.Commit()
}

// RollbackPublish reverts draftVersionID to draft and optionally restores restoreVersionID to
// released and active, in one transaction.
func (r *sqlVersionRepo) RollbackPublish(ctx context.Context, algorithmID, draftVersionID, restoreVersionID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lockedID string
	if err := tx.QueryRowContext(ctx, mapper.SQL("VersionLockAlgorithm"), algorithmID).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errs.NotFoundf("algorithm not found: %s", algorithmID)
		}
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, mapper.SQL("VersionSetDraft"), draftVersionID, now); err != nil {
		return err
	}
	if restoreVersionID != "" {
		if _, err := tx.ExecContext(ctx, mapper.SQL("VersionRestoreActive"), restoreVersionID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ArchiveVersion marks a version as archived and clears its active flag.
func (r *sqlVersionRepo) ArchiveVersion(ctx context.Context, versionID string) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("VersionSetArchived"), versionID, time.Now())
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errs.NotFoundf("version not found: %s", versionID)
	}
	return nil
}

func (r *sqlVersionRepo) CountStatusByAlgorithm(ctx context.Context, algorithmID string) (map[string]int, error) {
	return r.queryStatusCounts(ctx, mapper.SQL("VersionStatusCountsByAlgorithm"), algorithmID)
}

func (r *sqlVersionRepo) GlobalStatusCounts(ctx context.Context) (map[string]int, error) {
	return r.queryStatusCounts(ctx, mapper.SQL("VersionGlobalStatusCounts"))
}

func (r *sqlVersionRepo) queryStatusCounts(ctx context.Context, query string, args ...any) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// DeleteChangeLogs deletes all change logs of a version (called on cascade deletion).
func (r *sqlVersionRepo) DeleteChangeLogs(ctx context.Context, versionID string) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("ChangeLogDeleteByVersionID"), versionID)
	return err
}

func (r *sqlVersionRepo) CreateChangeLog(ctx context.Context, log *entity.VersionChangeLog) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("ChangeLogCreate"),
		log.ID, log.AlgorithmID, log.VersionID, log.Version, log.Operator, log.Action, log.ChangeTime, log.ChangeDesc, log.FileMD5,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil // Change logs are written idempotently; ignore duplicates.
		}
		return err
	}
	return nil
}

func (r *sqlVersionRepo) ListChangeLog(ctx context.Context, versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, mapper.SQL("ChangeLogCountByVersionID"), versionID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, mapper.SQL("ChangeLogListByVersionID"), versionID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := []entity.VersionChangeLog{}
	for rows.Next() {
		var l entity.VersionChangeLog
		var version, operator, action, desc, md5 sql.NullString
		if err := rows.Scan(&l.ID, &l.AlgorithmID, &l.VersionID, &version, &operator, &action, &l.ChangeTime, &desc, &md5); err != nil {
			return nil, 0, err
		}
		l.Version = version.String
		l.Operator = operator.String
		l.Action = action.String
		l.ChangeDesc = desc.String
		l.FileMD5 = md5.String
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}
