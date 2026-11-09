package service

import (
	"context"
	"time"

	"awesomeProject/internal/domain/do/entity"
)

// SessionRepository 会话持久化接口（由仓储层 SQL 实现，main.go 注入 SessionService）。
// 定义在此领域包内，与 AlgorithmRepository 一致（接口归属领域，实现在 repository 包）。
type SessionRepository interface {
	Create(ctx context.Context, s *entity.Session) error
	Get(ctx context.Context, sessionID string) (*entity.Session, error)
	Touch(ctx context.Context, sessionID string, now, expiresAt time.Time) error
	Delete(ctx context.Context, sessionID string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
