package session

import (
	"context"
	"log"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/enum"
	repoiface "awesomeProject/internal/repository/service"
	"awesomeProject/pkg/utils"
)

// SessionService 会话编排：mint / 校验续期 / 过期清理（GC）。
// 平台是会话真相源，落库 PostgreSQL；算法进程仅被动接收 X-Session-Id 头。
type SessionService struct {
	repo repoiface.SessionRepository
	ttl  time.Duration
}

// NewSessionService 构造会话服务。ttl<=0 时回落到 entity.DefaultTTL。
func NewSessionService(repo repoiface.SessionRepository, ttl time.Duration) *SessionService {
	if ttl <= 0 {
		ttl = entity.DefaultTTL
	}
	return &SessionService{repo: repo, ttl: ttl}
}

// Mint 创建一个新会话并落库，返回带 sessionId 的会话。
func (s *SessionService) Mint(ctx context.Context, code string) (*entity.Session, error) {
	now := time.Now()
	sess := &entity.Session{
		SessionID:     utils.NewID("sess_"),
		AlgorithmCode: code,
		Status:        enum.StatusActive,
		RequestCount:  0,
		CreatedAt:     now,
		ExpiresAt:     now.Add(s.ttl),
		LastActiveAt:  now,
	}
	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Resolve 解析请求中的会话：
//   - 未提供 sessionId → 新建会话（isNew=true）
//   - 已提供且有效    → 续期并复用（isNew=false, session 为已刷新的会话）
//   - 已提供但无效    → 返回对应错误（ErrNotFound / ErrExpired / ErrCodeMismatch / ErrInactive）
func (s *SessionService) Resolve(ctx context.Context, code, providedID string) (*entity.Session, bool, error) {
	if providedID == "" {
		sess, err := s.Mint(ctx, code)
		if err != nil {
			return nil, false, err
		}
		return sess, true, nil
	}
	sess, err := s.repo.Get(ctx, providedID)
	if err != nil {
		return nil, false, err // 含 entity.ErrNotFound
	}
	if sess.AlgorithmCode != code {
		return nil, false, entity.ErrCodeMismatch
	}
	if sess.Status != enum.StatusActive {
		return nil, false, entity.ErrInactive
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.repo.Delete(ctx, sess.SessionID) // 过期会话顺手清掉
		return nil, false, entity.ErrExpired
	}
	// 有效：续期（刷新 last_active_at + expires_at）并累加计数
	now := time.Now()
	expires := now.Add(s.ttl)
	if err := s.repo.Touch(ctx, sess.SessionID, now, expires); err != nil {
		return nil, false, err
	}
	sess.LastActiveAt = now
	sess.ExpiresAt = expires
	return sess, false, nil
}

// StartGC 启动后台过期会话清理（非阻塞）。interval<=0 时回落到 5 分钟。
func (s *SessionService) StartGC(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := s.repo.DeleteExpired(context.Background(), time.Now())
				if err != nil {
					log.Printf("[session] 清理过期会话失败: %v", err)
				} else if n > 0 {
					log.Printf("[session] 清理过期会话 %d 个", n)
				}
			}
		}
	}()
}
