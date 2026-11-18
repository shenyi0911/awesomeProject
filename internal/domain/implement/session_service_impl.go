package implement

import (
	"context"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/port"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

// sessionService handles session orchestration: mint, validation and renewal, and expiry cleanup (GC).
// The platform is the source of truth for sessions and persists them in PostgreSQL; algorithm
// processes only receive the X-Session-Id header.
type sessionService struct {
	repo port.SessionRepository
	ttl  time.Duration
	// maxPerCode is the maximum number of active sessions per algorithm; <=0 means unlimited (default).
	maxPerCode int
}

// NewSessionService creates the session service. ttl<=0 falls back to entity.DefaultTTL;
// maxPerCode<0 is treated as 0 (unlimited).
func NewSessionService(repo port.SessionRepository, ttl time.Duration, maxPerCode int) *sessionService {
	if ttl <= 0 {
		ttl = entity.DefaultTTL
	}
	if maxPerCode < 0 {
		maxPerCode = 0
	}
	return &sessionService{repo: repo, ttl: ttl, maxPerCode: maxPerCode}
}

// mint creates and persists a new session, returning it with its session ID.
func (s *sessionService) mint(ctx context.Context, code string) (*entity.Session, error) {
	now := time.Now()
	sess := &entity.Session{
		SessionID:     utils.NewID("sess_"),
		AlgorithmCode: code,
		Status:        enum.StatusActive,
		// Creation counts as the first request; together with request_count+1 in Touch,
		// RequestCount is the total number of calls served by the session.
		RequestCount: 1,
		CreatedAt:    now,
		ExpiresAt:    now.Add(s.ttl),
		LastActiveAt: now,
	}
	if s.maxPerCode > 0 {
		// Atomic quota: return ErrTooMany at the limit to avoid exceeding it under concurrency.
		if err := s.repo.CreateLimited(ctx, sess, s.maxPerCode); err != nil {
			return nil, err
		}
		return sess, nil
	}
	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Resolve resolves the session for a request.
func (s *sessionService) Resolve(ctx context.Context, code, providedID string) (*entity.Session, bool, error) {
	if providedID == "" {
		sess, err := s.mint(ctx, code)
		if err != nil {
			return nil, false, err
		}
		return sess, true, nil
	}
	sess, err := s.repo.Get(ctx, providedID)
	if err != nil {
		return nil, false, err // Includes entity.ErrNotFound.
	}
	if sess.AlgorithmCode != code {
		return nil, false, entity.ErrCodeMismatch
	}
	if sess.Status != enum.StatusActive {
		return nil, false, entity.ErrInactive
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.repo.Delete(ctx, sess.SessionID) // Delete the expired session while we are here.
		return nil, false, entity.ErrExpired
	}
	// Valid: renew (refresh last_active_at and expires_at) and increment the request count.
	now := time.Now()
	expires := now.Add(s.ttl)
	if err := s.repo.Touch(ctx, sess.SessionID, now, expires); err != nil {
		return nil, false, err
	}
	sess.LastActiveAt = now
	sess.ExpiresAt = expires
	return sess, false, nil
}

// DeleteByCode removes all sessions of an algorithm (called when the algorithm is deleted).
func (s *sessionService) DeleteByCode(ctx context.Context, code string) error {
	return s.repo.DeleteByCode(ctx, code)
}

// StartGC starts background cleanup of expired sessions (non-blocking). interval<=0 falls back to 5 minutes.
func (s *sessionService) StartGC(ctx context.Context, interval time.Duration) {
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
				n, err := s.repo.DeleteExpired(ctx, time.Now())
				if err != nil {
					logger.Warn("clean expired sessions failed", "error", err)
				} else if n > 0 {
					logger.Info("expired sessions cleaned", "count", n)
				}
			}
		}
	}()
}
