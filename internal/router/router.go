package router

import (
	"github.com/gin-gonic/gin"

	"awesomeProject/internal/handler"
	"awesomeProject/pkg/response"
)

// Dependencies 路由依赖
type Dependencies struct {
	AlgorithmHandler *handler.AlgorithmHandler
	VersionHandler   *handler.VersionHandler
	InfoHandler      *handler.InfoHandler
	CalculateHandler *handler.CalculateHandler
}

// New 创建并配置 Gin 引擎
func New(deps *Dependencies) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(gin.Logger())
	engine.Use(corsMiddleware())

	// 健康检查
	engine.GET("/health", func(c *gin.Context) {
		response.Success(c, gin.H{"status": "up"})
	})

	// API 路由分组
	api := engine.Group("/api/v1")
	{
		deps.AlgorithmHandler.RegisterRoutes(api)
		deps.VersionHandler.RegisterRoutes(api)
		deps.InfoHandler.RegisterRoutes(api)
		if deps.CalculateHandler != nil {
			deps.CalculateHandler.RegisterRoutes(api)
		}
	}

	// 404 处理
	engine.NoRoute(func(c *gin.Context) {
		response.Error(c, response.CodeNotFound, "route not found")
	})

	return engine
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
