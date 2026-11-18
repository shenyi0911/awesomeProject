package handler

import (
	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/response"
)

// InfoHandler handles information queries.
type InfoHandler struct {
	svc service.InfoService
}

// NewInfoHandler creates the information query handler.
func NewInfoHandler(svc service.InfoService) *InfoHandler {
	return &InfoHandler{svc: svc}
}

// RegisterRoutes registers the routes.
func (h *InfoHandler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/info")
	{
		group.GET("/algorithms/:id", h.GetAlgorithmInfo)
		group.GET("/dashboard", h.GetDashboardStats)
	}
}

// GetAlgorithmInfo returns aggregated information for an algorithm.
func (h *InfoHandler) GetAlgorithmInfo(c *gin.Context) {
	info, err := h.svc.GetAlgorithmInfo(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, info)
}

// GetDashboardStats returns dashboard statistics.
func (h *InfoHandler) GetDashboardStats(c *gin.Context) {
	stats, err := h.svc.GetDashboardStats(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, stats)
}
