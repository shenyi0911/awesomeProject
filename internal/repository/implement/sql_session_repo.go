package implement

import (
	"context"
	"database/sql"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/repository/mapper"
)

// sqlSessionRepo is the PostgreSQL implementation of the session repository.
// SQL statements live in mapper/sql/session.sql (annotated with -- name: Xxx); this file only
// fetches, binds and scans.
type sqlSessionRepo struct {
	db *sql.DB
}

var _ port.SessionRepository = (*sqlSessionRepo)(nil)

// NewSQLSessionRepository creates the session repository.
func NewSQLSessionRepository(db *sql.DB) *sqlSessionRepo {
	return &sqlSessionRepo{db: db}
}

func (r *sqlSessionRepo) Create(ctx context.Context, s *entity.Session) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("SessionCreate"),
		s.SessionID, s.AlgorithmCode, s.Status, s.RequestCount, s.CreatedAt, s.ExpiresAt, s.LastActiveAt)
	return err
}

// CreateLimited inserts atomically and returns entity.ErrTooMany when the active session count reaches limit.
// A transaction-scoped advisory lock serializes the count+insert pair per algorithm.
func (r *sqlSessionRepo) CreateLimited(ctx context.Context, s *entity.Session, limit int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, mapper.SQL("SessionLockCode"), s.AlgorithmCode); err != nil {
		return err
	}
	var active int
	if err := tx.QueryRowContext(ctx, mapper.SQL("SessionCountActive"), s.AlgorithmCode, s.CreatedAt).Scan(&active); err != nil {
		return err
	}
	if active >= limit {
		return entity.ErrTooMany
	}
	if _, err := tx.ExecContext(ctx, mapper.SQL("SessionCreateLimitedInsert"),
		s.SessionID, s.AlgorithmCode, s.Status, s.RequestCount, s.CreatedAt, s.ExpiresAt, s.LastActiveAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sqlSessionRepo) Get(ctx context.Context, sessionID string) (*entity.Session, error) {
	s := &entity.Session{}
	err := r.db.QueryRowContext(ctx, mapper.SQL("SessionGet"), sessionID).
		Scan(&s.SessionID, &s.AlgorithmCode, &s.Status, &s.RequestCount, &s.CreatedAt, &s.ExpiresAt, &s.LastActiveAt)
	if err == sql.ErrNoRows {
		return nil, entity.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Touch refreshes the last-active and expiry times and increments the request count when a session is reused.
func (r *sqlSessionRepo) Touch(ctx context.Context, sessionID string, now, expiresAt time.Time) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("SessionTouch"), now, expiresAt, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return entity.ErrNotFound
	}
	return nil
}

func (r *sqlSessionRepo) Delete(ctx context.Context, sessionID string) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("SessionDelete"), sessionID)
	return err
}

// DeleteByCode removes all sessions of an algorithm (called when the algorithm is deleted).
func (r *sqlSessionRepo) DeleteByCode(ctx context.Context, code string) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("SessionDeleteByCode"), code)
	return err
}

// DeleteExpired removes all expired sessions and returns the number deleted (called by background GC).
func (r *sqlSessionRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, mapper.SQL("SessionDeleteExpired"), now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
