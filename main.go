package main

import (
	"awesomeProject/internal/domain/session"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/config"
	"awesomeProject/internal/domain/implement"
	"awesomeProject/internal/filestorage"
	"awesomeProject/internal/handler"
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

	// 仓储层：PostgreSQL 直连（dao 只管连接/建表，仓储 SQL 实现在 implement 子包）
	db, err := dao.OpenSQLDB(cfg.Repository.SQL.Driver, cfg.Repository.SQL.DSN)
	if err != nil {
		logger.Error("open sql repository failed", "error", err)
		os.Exit(1)
	}
	algoRepo := repoimpl.NewSQLAlgorithmRepository(db)
	verRepo := repoimpl.NewSQLVersionRepository(db)
	infoRepo := repoimpl.NewInfoRepository(algoRepo, verRepo)
	sessRepo := repoimpl.NewSQLSessionRepository(db)

	// 文件存储：S3 兼容对象存储（当前后端 RustFS，minio-go/v7 通用 S3 客户端）
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

	// 服务层（注意顺序：verSvc 依赖 algoRepo；algoSvc 依赖 verSvc 以实现创建算法时自动生成 v1）
	verSvc := implement.NewVersionService(algoRepo, verRepo, fileStorage)
	algoSvc := implement.NewAlgorithmService(algoRepo, verSvc, fileStorage)
	infoSvc := implement.NewInfoService(infoRepo)

	// 会话服务（有状态算法状态关联）：落库 PostgreSQL，后台定时清理过期会话
	sessSvc := session.NewSessionService(sessRepo, envDuration("SESSION_TTL", 30*time.Minute))
	sessSvc.StartGC(context.Background(), envDuration("SESSION_GC_INTERVAL", 5*time.Minute))

	// 算法运行时编排器（执行层，合并进本进程）：
	// 算法文件以「已发布版本」为单位存储在 S3 兼容对象存储（当前后端 RustFS），平台是真相源，运行时不依赖任何固定目录。
	// 启动时按「最新版本=released」的算法登记并拉起；「发布」动作触发拉起、「归档」动作在其已无已发布版本时回收。
	// 拉起时从 MinIO 下载该已发布版本全部文件到本地暂存目录，解析其中 algorithm.yaml 得到启动契约后用 dapr run 拉起。
	// 通过同进程 PostgreSQL 直接做「发布状态闸门」校验（不含懒拉起/空闲回收/热监听）。
	rtMgr := runtime.New(runtime.Options{
		Workspace:           envOr("RUNTIME_WORKSPACE", runtime.DefaultWorkspace()),
		DaprBin:             envOr("DAPR_BIN", "dapr"),
		DefaultAlgo:         envOr("DEFAULT_ALGO", "surveyRD"),
		ReleaseCheckEnabled: isOn(envOr("RUNTIME_RELEASE_CHECK", "on")),
		ReleaseCacheTTL:     envDuration("RUNTIME_RELEASE_CACHE_TTL", 30*time.Second),
		StatusQuerier:       &runtime.SQLStatusQuerier{DB: db},
		Resolver:            verRepo.(runtime.VersionResolver), // verRepo 动态类型 *sqlVersionRepo 实现该接口
		Downloader:          fileStorage,                       // s3FileStorage 满足 BlobDownloader
		PortResolver:        algoRepo,                          // *sqlAlgorithmRepo 实现 PortForCode（按 code 取平台指定端口）
	})
	rtMgr.Start()

	// 处理器
	deps := &router.Dependencies{
		AlgorithmHandler: handler.NewAlgorithmHandler(algoSvc),
		VersionHandler:   handler.NewVersionHandler(verSvc, rtMgr),
		InfoHandler:      handler.NewInfoHandler(infoSvc),
		CalculateHandler: handler.NewCalculateHandler(rtMgr, algoSvc, sessSvc),
	}

	// 优雅退出：清理算法子进程（dapr + 算法本体）
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		logger.Info("收到退出信号，清理算法子进程...")
		rtMgr.Stop()
		_ = db.Close()
		os.Exit(0)
	}()

	// 启动服务
	engine := router.New(deps)
	logger.Info("server starting", "addr", cfg.Addr(), "mode", cfg.Server.Mode, "repository", cfg.Repository.Type)
	if err := engine.Run(cfg.Addr()); err != nil {
		logger.Error("server run failed", "error", err)
		os.Exit(1)
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

// envOr 取环境变量，缺失时返回默认值。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func isOn(v string) bool {
	lv := strings.ToLower(strings.TrimSpace(v))
	return lv == "on" || lv == "true" || lv == "1" || lv == "yes"
}
