package port

import (
	"context"
	"io"
	"net/http"
)

// InstanceInfo is a read-only snapshot of a running instance; callers must not hold runtime internals.
type InstanceInfo struct {
	Code      string
	Version   string
	VersionID string
	Healthy   bool
}

// Runtime is the algorithm runtime port (the execution-layer orchestrator).
type Runtime interface {
	Start(ctx context.Context)
	Stop()
	Launch(ctx context.Context, code string) error
	MaybeKill(ctx context.Context, code string)
	// Kill reclaims the algorithm process immediately, regardless of its release state.
	Kill(code string)
	Invoke(ctx context.Context, code, daprMethod, httpMethod string, body []byte, headers http.Header) ([]byte, int, error)
	Instance(code string) (InstanceInfo, bool)
	ReleaseCheck(ctx context.Context, code string) (ok bool, httpStatus int, msg string)
	// InvalidateRelease clears the release-state cache for an algorithm so publish/archive takes effect immediately.
	InvalidateRelease(code string)
}

// FileRef is a version file reference in object storage.
type FileRef struct {
	Key     string // Object key, e.g. versions/{vid}/{uuid}/{rel}.
	RelPath string // Relative path used to rebuild the directory structure.
}

// VersionResolver provides the runtime with access to the source of truth for versions (implemented by the repository layer).
type VersionResolver interface {
	LatestReleasedVersion(ctx context.Context, code string) (versionID, version, status string, err error)
	VersionFiles(ctx context.Context, versionID string) ([]FileRef, error)
	ListReleasedCodes(ctx context.Context) ([]string, error)
}

// BlobDownloader provides object storage downloads (implemented by filestorage).
type BlobDownloader interface {
	Download(ctx context.Context, key string) (io.ReadCloser, error)
}

// PortResolver reads and writes algorithm listen ports (implemented by the algorithm repository).
type PortResolver interface {
	PortForCode(ctx context.Context, code string) (int, error)
	SetPort(ctx context.Context, code string, port int) error
}
