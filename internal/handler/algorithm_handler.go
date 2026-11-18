package handler

import (
	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/response"
	"awesomeProject/pkg/utils"
)

// AlgorithmHandler handles algorithm registration.
type AlgorithmHandler struct {
	svc service.AlgorithmService
}

// NewAlgorithmHandler creates the algorithm handler.
func NewAlgorithmHandler(svc service.AlgorithmService) *AlgorithmHandler {
	return &AlgorithmHandler{svc: svc}
}

// RegisterRoutes registers the routes.
func (h *AlgorithmHandler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/algorithms")
	{
		group.POST("", h.Register)
		group.PUT("/:id", h.Update)
		group.DELETE("/:id", h.Delete)
		group.GET("/:id", h.GetByID)
		group.GET("/code/:code", h.GetByCode)
		group.GET("", h.List)
	}
}

// Register registers an algorithm; afterwards create v1 and upload files from the detail page.
func (h *AlgorithmHandler) Register(c *gin.Context) {
	var req request.RegisterAlgorithmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	algo, err := h.svc.Register(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "registered", algo)
}

// Update updates an algorithm.
func (h *AlgorithmHandler) Update(c *gin.Context) {
	var req request.UpdateAlgorithmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	// The update user always comes from the authenticated context, never from the request body.
	req.UpdateUser = operatorOf(c)
	algo, err := h.svc.Update(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "updated", algo)
}

// Delete deletes an algorithm; only allowed when its latest version is draft or archived.
func (h *AlgorithmHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "deleted", nil)
}

// GetByID returns an algorithm by ID.
func (h *AlgorithmHandler) GetByID(c *gin.Context) {
	algo, err := h.svc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, algo)
}

// GetByCode returns an algorithm by code.
func (h *AlgorithmHandler) GetByCode(c *gin.Context) {
	algo, err := h.svc.GetByCode(c.Request.Context(), c.Param("code"))
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, algo)
}

// List returns a page of algorithms.
func (h *AlgorithmHandler) List(c *gin.Context) {
	var req request.ListAlgorithmRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	req.Limit, req.Offset = utils.NormalizePaging(req.Limit, req.Offset)
	list, total, err := h.svc.List(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, gin.H{
		"list":   list,
		"total":  total,
		"limit":  req.Limit,
		"offset": req.Offset,
	})
}
