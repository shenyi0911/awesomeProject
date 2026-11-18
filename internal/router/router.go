package router

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/handler"
	"awesomeProject/internal/metrics"
	"awesomeProject/internal/middleware"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/response"
)

// readinessTimeout bounds a single readiness probe.
const readinessTimeout = 5 * time.Second

// Dependencies holds the handlers and middleware options used to build the router.
type Dependencies struct {
	AlgorithmHandler *handler.AlgorithmHandler
	VersionHandler   *handler.VersionHandler
	InfoHandler      *handler.InfoHandler
	CalculateHandler *handler.CalculateHandler

	// Readiness checks downstream dependencies (database, object storage) for /ready.
	Readiness func(context.Context) error

	AuthToken        string
	AuthOperator     string
	CORSAllowOrigins []string
	MaxJSONBody      int64
	MaxUploadBody    int64
	MaxFileBytes     int64
	MultipartMemory  int64
	Metrics          *metrics.Metrics
}

// New creates and configures the Gin engine.
func New(deps *Dependencies) *gin.Engine {
	engine := gin.New()
	if deps.MultipartMemory > 0 {
		engine.MaxMultipartMemory = deps.MultipartMemory
	}
	// Middleware order: Recovery -> RequestID -> AccessLog -> CORS -> BodyLimit -> Auth.
	engine.Use(middleware.Recovery())
	engine.Use(middleware.RequestID())
	engine.Use(middleware.AccessLog())
	engine.Use(middleware.Metrics(deps.Metrics))
	engine.Use(middleware.CORS(deps.CORSAllowOrigins))
	engine.Use(middleware.BodyLimit(deps.MaxJSONBody, deps.MaxUploadBody))
	if deps.AuthToken != "" {
		engine.Use(middleware.BearerAuth(deps.AuthToken, deps.AuthOperator))
	} else {
		logger.Warn("API authentication is disabled (auth.token is empty); use only in internal/dev environments")
	}

	// Health check (BearerAuth already allows /health).
	engine.GET("/health", func(c *gin.Context) {
		response.Success(c, gin.H{"status": "up"})
	})
	if deps.Metrics != nil {
		engine.GET("/metrics", gin.WrapH(deps.Metrics.Handler()))
	}
	// Readiness check: liveness stays independent of downstream dependencies.
	engine.GET("/ready", func(c *gin.Context) {
		if deps.Readiness != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
			defer cancel()
			if err := deps.Readiness(ctx); err != nil {
				logger.Warn("readiness check failed", "error", err)
				response.Error(c, response.CodeServiceUnavailable, "not ready")
				return
			}
		}
		response.Success(c, gin.H{"status": "ready"})
	})

	api := engine.Group("/api/v1")
	{
		deps.AlgorithmHandler.RegisterRoutes(api)
		deps.VersionHandler.RegisterRoutes(api)
		deps.InfoHandler.RegisterRoutes(api)
		if deps.CalculateHandler != nil {
			deps.CalculateHandler.RegisterRoutes(api)
		}
	}

	engine.NoRoute(func(c *gin.Context) {
		response.Error(c, response.CodeNotFound, "route not found")
	})
	return engine
}
