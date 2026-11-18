package service

import (
	"context"
	"time"

	"awesomeProject/internal/domain/do/entity"
)

// SessionService handles session orchestration (mint, validation and renewal, expiry cleanup).
type SessionService interface {
	// Resolve resolves the session for a request:
	//   - no sessionId provided -> creates a new session (isNew=true)
	//   - provided and valid    -> renews and reuses it (isNew=false)
	//   - provided but invalid  -> returns the matching error (ErrNotFound / ErrExpired / ErrCodeMismatch / ErrInactive / ErrTooMany)
	Resolve(ctx context.Context, code, providedID string) (*entity.Session, bool, error)
	// DeleteByCode removes every session of an algorithm (used when the algorithm is deleted).
	DeleteByCode(ctx context.Context, code string) error
	// StartGC starts background cleanup of expired sessions (non-blocking).
	StartGC(ctx context.Context, interval time.Duration)
}
