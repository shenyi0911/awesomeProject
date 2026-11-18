package implement

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/repository/mapper"
)

// sqlAlgorithmRepo is the PostgreSQL implementation of the algorithm repository.
// SQL statements live in mapper/sql/algorithm.sql (annotated with -- name: Xxx); this file only
// fetches, binds and scans.
type sqlAlgorithmRepo struct {
	db *sql.DB
}

var _ port.AlgorithmRepository = (*sqlAlgorithmRepo)(nil)

// NewSQLAlgorithmRepository creates the SQL algorithm repository.
func NewSQLAlgorithmRepository(db *sql.DB) *sqlAlgorithmRepo {
	return &sqlAlgorithmRepo{db: db}
}

// isUniqueViolation reports whether err is a unique constraint violation (PostgreSQL code 23505).
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}

func (r *sqlAlgorithmRepo) Create(ctx context.Context, algo *entity.Algorithm) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("AlgorithmCreate"),
		algo.ID, algo.Name, algo.Code, algo.Description, algo.Category,
		algo.Author, string(algo.Language), algo.Scene, algo.Stateful,
		algo.UpdateUser, algo.Port, algo.CreatedAt, algo.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return errs.New(errs.KindConflict, "algorithm code already exists: "+algo.Code)
		}
		return err
	}
	return nil
}

func (r *sqlAlgorithmRepo) Update(ctx context.Context, algo *entity.Algorithm) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("AlgorithmUpdate"),
		algo.ID, algo.Name, algo.Code, algo.Description, algo.Category,
		algo.Author, string(algo.Language), algo.Scene, algo.Stateful,
		algo.Port, algo.UpdateUser, time.Now(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return errs.New(errs.KindConflict, "algorithm code already exists: "+algo.Code)
		}
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errs.NotFoundf("algorithm not found: %s", algo.ID)
	}
	return nil
}

func (r *sqlAlgorithmRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("AlgorithmDelete"), id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		// The guard prevents deleting an algorithm that still has an active released version.
		return errs.New(errs.KindConflict, "algorithm has a released version and cannot be deleted; archive it first")
	}
	return nil
}

// scanAlgorithm scans an algorithm from one row, optionally including the latest version and status.
func scanAlgorithm(scan func(...any) error, withLatest bool) (*entity.Algorithm, error) {
	a := &entity.Algorithm{}
	var name, code, desc, category, author, lang, scene, updateUser sql.NullString
	var latestVersion, latestStatus sql.NullString
	dest := []any{&a.ID, &name, &code, &desc, &category, &author, &lang, &scene,
		&a.Stateful, &updateUser, &a.Port, &a.CreatedAt, &a.UpdatedAt}
	if withLatest {
		dest = append(dest, &latestVersion, &latestStatus)
	}
	if err := scan(dest...); err != nil {
		return nil, err
	}
	a.Name = name.String
	a.Code = code.String
	a.Description = desc.String
	a.Category = category.String
	a.Author = author.String
	a.Language = enum.Language(lang.String)
	a.Scene = scene.String
	a.UpdateUser = updateUser.String
	a.LatestVersion = latestVersion.String
	a.LatestVersionStatus = latestStatus.String
	return a, nil
}

func (r *sqlAlgorithmRepo) GetByID(ctx context.Context, id string) (*entity.Algorithm, error) {
	a, err := scanAlgorithm(r.db.QueryRowContext(ctx, mapper.SQL("AlgorithmGetByID"), id).Scan, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errs.NotFoundf("algorithm not found: %s", id)
		}
		return nil, err
	}
	return a, nil
}

func (r *sqlAlgorithmRepo) GetByCode(ctx context.Context, code string) (*entity.Algorithm, error) {
	a, err := scanAlgorithm(r.db.QueryRowContext(ctx, mapper.SQL("AlgorithmGetByCode"), code).Scan, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errs.NotFoundf("algorithm not found: %s", code)
		}
		return nil, err
	}
	return a, nil
}

// List returns a page of algorithms with the latest version and status.
// Paging is normalized by the caller (service); the repository does not truncate, so internal full scans are not silently limited.
func (r *sqlAlgorithmRepo) List(ctx context.Context, filter request.AlgorithmFilter) ([]entity.Algorithm, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, mapper.SQL("AlgorithmCountByFilter"),
		filter.Name, filter.Code).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, mapper.SQL("AlgorithmListByFilter"),
		filter.Name, filter.Code, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	result := []entity.Algorithm{}
	for rows.Next() {
		a, err := scanAlgorithm(rows.Scan, true)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

// ListAll returns all algorithms (used by internal rules and stats).
func (r *sqlAlgorithmRepo) ListAll(ctx context.Context) ([]entity.Algorithm, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("AlgorithmListAll"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []entity.Algorithm{}
	for rows.Next() {
		a, err := scanAlgorithm(rows.Scan, true)
		if err != nil {
			return nil, err
		}
		result = append(result, *a)
	}
	return result, rows.Err()
}

func (r *sqlAlgorithmRepo) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, mapper.SQL("AlgorithmCountAll")).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *sqlAlgorithmRepo) CategoryCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("AlgorithmCategoryCounts"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var category string
		var n int
		if err := rows.Scan(&category, &n); err != nil {
			return nil, err
		}
		out[category] = n
	}
	return out, rows.Err()
}

// PortForCode returns the listen port recorded for the algorithm.
func (r *sqlAlgorithmRepo) PortForCode(ctx context.Context, code string) (int, error) {
	var port int
	if err := r.db.QueryRowContext(ctx, mapper.SQL("AlgorithmPortForCode"), code).Scan(&port); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errs.NotFoundf("algorithm not found: %s", code)
		}
		return 0, err
	}
	return port, nil
}

// SetPort writes back the listen port actually assigned by the runtime.
func (r *sqlAlgorithmRepo) SetPort(ctx context.Context, code string, port int) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("AlgorithmSetPort"), port, code)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errs.NotFoundf("algorithm not found: %s", code)
	}
	return nil
}
