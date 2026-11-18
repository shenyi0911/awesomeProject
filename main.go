package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/app"
	"awesomeProject/internal/config"
	"awesomeProject/internal/domain/implement"
	"awesomeProject/internal/filestorage"
	"awesomeProject/internal/handler"
	platformmetrics "awesomeProject/internal/metrics"
	"awesomeProject/internal/repository/dao"
	repoimpl "awesomeProject/internal/repository/implement"
	"awesomeProject/internal/router"
	"awesomeProject/internal/runtime"
	"awesomeProject/pkg/logger"
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	setupLogger(cfg.Log.Level)
	gin.SetMode(cfg.Server.Mode)

	// Startup dependency initialization (database connection, schema, bucket) shares a single timeout.
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelStartup()

	db, err := dao.OpenSQLDB(startupCtx, cfg.Repository.SQL.Driver, cfg.Repository.SQL.DSN)
	if err != nil {
		logger.Error("open sql repository failed", "error", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(cfg.Repository.SQL.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Repository.SQL.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.Repository.SQL.ConnMaxLifetime.D())
	db.SetConnMaxIdleTime(cfg.Repository.SQL.ConnMaxIdleTime.D())

	algoRepo := repoimpl.NewSQLAlgorithmRepository(db)
	verRepo := repoimpl.NewSQLVersionRepository(db)
	infoRepo := repoimpl.NewInfoRepository(algoRepo, verRepo)
	sessRepo := repoimpl.NewSQLSessionRepository(db)

	fileStorage, err := filestorage.NewS3FileStorage(
		cfg.FileStorage.S3.Endpoint,
		cfg.FileStorage.S3.AccessKey,
		cfg.FileStorage.S3.SecretKey,
		cfg.FileStorage.S3.Bucket,
		cfg.FileStorage.S3.UseSSL,
	)
	if err != nil {
		logger.Error("init s3 file storage failed", "error", err)
		os.Exit(1)
	}

	// Service layer: algoSvc depends on verSvc to cascade version cleanup when an algorithm is deleted.
	verSvc := implement.NewVersionService(algoRepo, verRepo, fileStorage)
	infoSvc := implement.NewInfoService(infoRepo)

	// Session service: sessions are persisted in PostgreSQL and expired ones are purged periodically.
	sessSvc := implement.NewSessionService(sessRepo, cfg.Session.TTL.D(), cfg.Session.MaxPerAlgorithm)

	lifeCtx, stopLife := context.WithCancel(context.Background())
	sessSvc.StartGC(lifeCtx, cfg.Session.GCInterval.D())

	// Runtime orchestrator: launches published algorithms on startup and starts/stops processes on publish/archive.
	platformMetrics := platformmetrics.New()
	rtMgr := runtime.New(runtime.Options{
		Workspace:           cfg.Runtime.Workspace,
		DaprBin:             cfg.Runtime.DaprBin,
		ReleaseCheckEnabled: cfg.Runtime.ReleaseCheck,
		ReleaseCacheTTL:     cfg.Runtime.ReleaseCacheTTL.D(),
		InvokeTimeout:       cfg.Runtime.InvokeTimeout.D(),
		MaxRestarts:         cfg.Runtime.MaxRestarts,
		Resolver:            verRepo,
		Downloader:          fileStorage,
		PortResolver:        algoRepo,
		ObserveAlgorithm: func(code, method string, d time.Duration) {
			platformMetrics.ObserveInvokePhase(code, method, platformmetrics.PhaseAlgorithm, d)
		},
	})
	algoSvc := implement.NewAlgorithmService(algoRepo, verSvc, sessSvc, fileStorage, rtMgr)
	rtMgr.Start(lifeCtx)

	// Application layer: orchestration for publish/launch/rollback and invoke/session/forward.
	versionApp := app.NewVersionApp(verSvc, rtMgr)
	invokeApp := app.NewInvokeApp(rtMgr, algoSvc, sessSvc, cfg.Session.StatefulCacheTTL.D(), platformMetrics)
	// Algorithm updates/deletes must drop cached invocation metadata immediately.
	algoSvc.SetMetaInvalidator(invokeApp.InvalidateMeta)

	deps := &router.Dependencies{
		AlgorithmHandler: handler.NewAlgorithmHandler(algoSvc),
		VersionHandler:   handler.NewVersionHandler(verSvc, versionApp, cfg.Server.MaxFileBytes),
		InfoHandler:      handler.NewInfoHandler(infoSvc),
		CalculateHandler: handler.NewCalculateHandler(invokeApp),
		Readiness: func(ctx context.Context) error {
			if err := db.PingContext(ctx); err != nil {
				return fmt.Errorf("database ping failed: %w", err)
			}
			if _, err := exec.LookPath(cfg.Runtime.DaprBin); err != nil {
				return fmt.Errorf("dapr binary %q not found: %w", cfg.Runtime.DaprBin, err)
			}
			workspace := cfg.Runtime.Workspace
			if workspace == "" {
				workspace = runtime.DefaultWorkspace()
			}
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				return fmt.Errorf("runtime workspace is unavailable: %w", err)
			}
			f, err := os.CreateTemp(workspace, ".ready-*")
			if err != nil {
				return fmt.Errorf("runtime workspace is not writable: %w", err)
			}
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return fileStorage.Health(ctx)
		},
		AuthToken:        cfg.Auth.Token,
		AuthOperator:     cfg.Auth.Operator,
		CORSAllowOrigins: cfg.Server.CORSAllowOrigins,
		MaxJSONBody:      cfg.Server.MaxJSONBody,
		MaxUploadBody:    cfg.Server.MaxUploadBody,
		MaxFileBytes:     cfg.Server.MaxFileBytes,
		MultipartMemory:  cfg.Server.MaxMultipartMemory,
		Metrics:          platformMetrics,
	}

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router.New(deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout.D(),
		WriteTimeout:      cfg.Server.WriteTimeout.D(),
		IdleTimeout:       cfg.Server.IdleTimeout.D(),
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", cfg.Addr(), "mode", cfg.Server.Mode,
			"repository", cfg.Repository.Type, "auth", cfg.Auth.Token != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	exitCode := 0
	select {
	case err := <-serverErr:
		logger.Error("server run failed", "error", err)
		exitCode = 1
	case s := <-sig:
		logger.Info("received shutdown signal, shutting down gracefully", "signal", s.String())
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.D())
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown failed", "error", err)
	}
	// Stop background tasks and algorithm processes before closing the database.
	stopLife()
	rtMgr.Stop()
	if err := db.Close(); err != nil {
		logger.Warn("close db failed", "error", err)
	}
	logger.Info("server exited")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func setupLogger(levelStr string) {
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger.SetLevel(level)
}
