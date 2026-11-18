package service

import (
	"context"
	"io"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
)

// VersionService manages algorithm versions.
type VersionService interface {
	Create(ctx context.Context, req request.CreateVersionRequest) (*entity.AlgorithmVersion, error)
	Update(ctx context.Context, id string, req request.UpdateVersionRequest) (*entity.AlgorithmVersion, error)
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.AlgorithmVersion, error)
	// AlgorithmCode returns the algorithm code for an algorithm ID, used by the runtime to locate the package and Dapr app ID.
	AlgorithmCode(ctx context.Context, algoID string) (string, error)
	List(ctx context.Context, req request.ListVersionRequest) ([]entity.AlgorithmVersion, int, error)
	// ListAll returns all versions of an algorithm (used by internal rules and cascade deletion).
	ListAll(ctx context.Context, algorithmID string) ([]entity.AlgorithmVersion, error)
	GetActive(ctx context.Context, algorithmID string) (*entity.AlgorithmVersion, error)
	// Status flow: draft -> released -> archived. Archived versions are immutable and cannot be deleted physically.
	Publish(ctx context.Context, id string, operator string) (*entity.AlgorithmVersion, error)
	Archive(ctx context.Context, id string, operator string) (*entity.AlgorithmVersion, error)
	// RevertPublish rolls a failed publish back to draft and restores the previously released
	// version (restoreVersionID may be empty).
	RevertPublish(ctx context.Context, versionID, restoreVersionID, operator string) error
	// Version-level files (each version owns an independent set).
	UploadFile(ctx context.Context, versionID string, in request.UploadFileInput, operator string) (*entity.AlgorithmFile, error)
	UploadFiles(ctx context.Context, versionID string, ins []request.UploadFileInput, operator string) ([]*entity.AlgorithmFile, error)
	DeleteFile(ctx context.Context, versionID, key, operator string) error
	GetFileURL(ctx context.Context, versionID, key string) (string, error)
	// BatchDownloadFiles streams the selected version files from object storage as a zip archive, preserving relative paths.
	BatchDownloadFiles(ctx context.Context, versionID string, keys []string) (io.ReadCloser, string, error)
	// Change logs.
	ListChangeLog(ctx context.Context, versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error)
}
