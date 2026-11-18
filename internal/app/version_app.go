// Package app is the application layer. It hosts business orchestration that spans domains and
// infrastructure (publish -> launch -> rollback, invoke -> session -> forward). Handlers only
// handle protocol conversion and no longer depend on runtime implementations.
package app

import (
	"context"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/logger"
)

// VersionApp orchestrates version publish and archive.
type VersionApp interface {
	Publish(ctx context.Context, versionID, operator string) (*entity.AlgorithmVersion, error)
	Archive(ctx context.Context, versionID, operator string) (*entity.AlgorithmVersion, error)
}

type versionApp struct {
	svc service.VersionService
	rt  port.Runtime
}

// NewVersionApp creates the version orchestration service.
func NewVersionApp(svc service.VersionService, rt port.Runtime) *versionApp {
	return &versionApp{svc: svc, rt: rt}
}

// launchTimeout bounds file download, dependency installation and health checks.
const launchTimeout = 5 * time.Minute

// Publish publishes a version and launches the algorithm process; if the launch fails it rolls back
// the publish state and restores the previously released version.
func (a *versionApp) Publish(ctx context.Context, versionID, operator string) (*entity.AlgorithmVersion, error) {
	v, err := a.svc.GetByID(ctx, versionID)
	if err != nil {
		return nil, err
	}
	code, err := a.svc.AlgorithmCode(ctx, v.AlgorithmID)
	if err != nil {
		return nil, err
	}

	// Record the active version before publishing so it can be restored if the launch fails.
	restoreID := ""
	if prev, perr := a.svc.GetActive(ctx, v.AlgorithmID); perr == nil && prev != nil && prev.ID != versionID {
		restoreID = prev.ID
	}

	published, err := a.svc.Publish(ctx, versionID, operator)
	if err != nil {
		return nil, err
	}

	// The launch must not be canceled by a client disconnect, but still needs an overall timeout.
	launchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), launchTimeout)
	defer cancel()
	if err := a.rt.Launch(launchCtx, code); err != nil {
		logger.Error("launch after publish failed; rolling back publish", "code", code, "version", versionID, "error", err)
		if rerr := a.svc.RevertPublish(launchCtx, versionID, restoreID, operator); rerr != nil {
			logger.Error("rollback publish status failed", "version", versionID, "error", rerr)
		}
		a.rt.InvalidateRelease(code)
		// Kill any process auto-restarted during the failed launch, avoiding a running but unpublished instance.
		a.rt.MaybeKill(launchCtx, code)
		return nil, errs.Wrap(errs.KindBadGateway, err, "publish failed: algorithm process launch failed; rolled back")
	}
	a.rt.InvalidateRelease(code)
	return published, nil
}

// Archive archives a version and reclaims the algorithm process when no released version remains.
func (a *versionApp) Archive(ctx context.Context, versionID, operator string) (*entity.AlgorithmVersion, error) {
	v, err := a.svc.Archive(ctx, versionID, operator)
	if err != nil {
		return nil, err
	}
	code, err := a.svc.AlgorithmCode(ctx, v.AlgorithmID)
	if err != nil {
		logger.Warn("resolve algorithm code after archive failed", "version", versionID, "error", err)
		return v, nil
	}
	a.rt.InvalidateRelease(code)
	a.rt.MaybeKill(ctx, code)
	return v, nil
}
