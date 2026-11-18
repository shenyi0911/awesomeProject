// Package port defines the ports used by the domain layer (repositories, storage, runtime).
// Ports are defined by consumers (domain/app) and implemented by adapters
// (repository/runtime/filestorage), which depend inward on this package.
package port

import (
	"context"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
)

// AlgorithmRepository is the port for algorithm persistence.
type AlgorithmRepository interface {
	Create(ctx context.Context, algo *entity.Algorithm) error
	Update(ctx context.Context, algo *entity.Algorithm) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.Algorithm, error)
	GetByCode(ctx context.Context, code string) (*entity.Algorithm, error)
	List(ctx context.Context, filter request.AlgorithmFilter) ([]entity.Algorithm, int, error)
	// ListAll returns all algorithms without pagination (used by internal rules and stats).
	ListAll(ctx context.Context) ([]entity.Algorithm, error)
	// Count returns the total number of algorithms.
	Count(ctx context.Context) (int, error)
	// CategoryCounts returns the algorithm count per category using a single SQL aggregation.
	CategoryCounts(ctx context.Context) (map[string]int, error)
	// PortForCode returns the listen port recorded for the algorithm, used when starting the runtime.
	PortForCode(ctx context.Context, code string) (int, error)
	// SetPort writes back the port actually assigned by the runtime so the UI can display it.
	SetPort(ctx context.Context, code string, port int) error
}

// VersionRepository is the port for version persistence.
// Metadata updates and file persistence are separated; status transitions are performed
// atomically by ActivateVersion and ArchiveVersion.
type VersionRepository interface {
	Create(ctx context.Context, version *entity.AlgorithmVersion) error
	UpdateMeta(ctx context.Context, version *entity.AlgorithmVersion) error
	// SaveFiles rewrites the version file set, incrementing the revision and failing with a
	// conflict when expectedRevision no longer matches (concurrent modification).
	SaveFiles(ctx context.Context, versionID, algorithmID string, files []entity.AlgorithmFile, expectedRevision int64) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.AlgorithmVersion, error)
	ListByAlgorithm(ctx context.Context, algorithmID string, filter request.VersionFilter) ([]entity.AlgorithmVersion, int, error)
	// ListAllByAlgorithm returns all versions of an algorithm without pagination (used by internal rules and stats).
	ListAllByAlgorithm(ctx context.Context, algorithmID string) ([]entity.AlgorithmVersion, error)
	GetActiveByAlgorithm(ctx context.Context, algorithmID string) (*entity.AlgorithmVersion, error)
	// ActivateVersion archives the other versions of the algorithm and marks the target
	// version as released and active, all within one transaction.
	ActivateVersion(ctx context.Context, algorithmID, versionID, parameterSpec string) error
	// RollbackPublish reverts draftVersionID to draft and, when set, restores restoreVersionID
	// to released and active, all within one transaction.
	RollbackPublish(ctx context.Context, algorithmID, draftVersionID, restoreVersionID string) error
	// ArchiveVersion marks a version as archived and clears its active flag.
	ArchiveVersion(ctx context.Context, versionID string) error
	CountStatusByAlgorithm(ctx context.Context, algorithmID string) (map[string]int, error)
	GlobalStatusCounts(ctx context.Context) (map[string]int, error)
	CreateChangeLog(ctx context.Context, log *entity.VersionChangeLog) error
	ListChangeLog(ctx context.Context, versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error)
	DeleteChangeLogs(ctx context.Context, versionID string) error
}

// InfoRepository is the port for read-only information queries.
type InfoRepository interface {
	GetAlgorithmInfo(ctx context.Context, algorithmID string) (*entity.AlgorithmInfo, error)
	GetStats(ctx context.Context) (entity.DashboardStats, error)
}

// SessionRepository is the port for session persistence.
type SessionRepository interface {
	Create(ctx context.Context, s *entity.Session) error
	// CreateLimited inserts atomically only when the algorithm has fewer than limit active
	// sessions; otherwise it returns entity.ErrTooMany.
	CreateLimited(ctx context.Context, s *entity.Session, limit int) error
	Get(ctx context.Context, sessionID string) (*entity.Session, error)
	Touch(ctx context.Context, sessionID string, now, expiresAt time.Time) error
	Delete(ctx context.Context, sessionID string) error
	// DeleteByCode removes every session of an algorithm (used when the algorithm is deleted).
	DeleteByCode(ctx context.Context, code string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
