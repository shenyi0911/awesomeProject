package handler

import (
	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/response"
)

// InfoHandler 信息查询处理器
type InfoHandler struct {
	svc service.InfoService
}

// NewInfoHandler 创建信息查询处理器
func NewInfoHandler(svc service.InfoService) *InfoHandler {
	return &InfoHandler{svc: svc}
}

// RegisterRoutes 注册路由
func (h *InfoHandler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/info")
	{
		group.GET("/algorithms/:id", h.GetAlgorithmInfo)
		group.GET("/dashboard", h.GetDashboardStats)
	}
}

// GetAlgorithmInfo 获取算法综合信息
func (h *InfoHandler) GetAlgorithmInfo(c *gin.Context) {
	id := c.Param("id")
	info, err := h.svc.GetAlgorithmInfo(id)
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, info)
}

// GetDashboardStats 获取仪表盘统计
func (h *InfoHandler) GetDashboardStats(c *gin.Context) {
	stats, err := h.svc.GetDashboardStats()
	if err != nil {
		response.Error(c, response.CodeInternalError, err.Error())
		return
	}
	response.Success(c, stats)
}
