package port

import (
	"context"
	"io"
	"time"
)

// FileStorage is the file storage port: algorithm file bytes live in an S3-compatible object store.
// The business layer depends only on this interface; the implementation is chosen by config.
type FileStorage interface {
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// Health reports whether the backing object store is reachable and the bucket is available.
	Health(ctx context.Context) error
}
