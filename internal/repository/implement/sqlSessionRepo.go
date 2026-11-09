package implement

import (
	"context"
	"database/sql"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/repository/mapper"
	"awesomeProject/internal/repository/service"
)

// sqlSessionRepo 基于 PostgreSQL 的会话仓储实现。
// SQL 全部声明在 mapper/sql/session.sql（-- name: Xxx 标注），此处只负责取语句、传参、扫描。
type sqlSessionRepo struct {
	db *sql.DB
}

// NewSQLSessionRepository 构造会话仓储（注入 SessionService）。
func NewSQLSessionRepository(db *sql.DB) service.SessionRepository {
	return &sqlSessionRepo{db: db}
}

func (r *sqlSessionRepo) Create(ctx context.Context, s *entity.Session) error {
	_, err := r.db.ExecContext(ctx, mapper.SQL("SessionCreate"),
		s.SessionID, s.AlgorithmCode, s.Status, s.RequestCount, s.CreatedAt, s.ExpiresAt, s.LastActiveAt)
	return err
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

// Touch 复用会话时刷新活跃时间与过期时间，并累加请求次数。
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

// DeleteExpired 删除所有已过期会话，返回清理条数（后台 GC 调用）。
func (r *sqlSessionRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, mapper.SQL("SessionDeleteExpired"), now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
