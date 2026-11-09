package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/response"
)

// AlgorithmHandler 算法注册处理器
type AlgorithmHandler struct {
	svc service.AlgorithmService
}

// NewAlgorithmHandler 创建算法处理器
func NewAlgorithmHandler(svc service.AlgorithmService) *AlgorithmHandler {
	return &AlgorithmHandler{svc: svc}
}

// RegisterRoutes 注册路由
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

// Register 注册算法（JSON：基础字段，创建后跳转到详情页手动新建 v1 并上传文件）
func (h *AlgorithmHandler) Register(c *gin.Context) {
	var req request.RegisterAlgorithmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}

	algo, err := h.svc.Register(c.Request.Context(), req)
	if err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	response.SuccessWithMessage(c, "registered", algo)
}

// Update 更新算法
func (h *AlgorithmHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var req request.UpdateAlgorithmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}

	algo, err := h.svc.Update(id, req)
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.SuccessWithMessage(c, "updated", algo)
}

// Delete 删除算法（仅允许最新版本为草稿/已归档状态的算法）
func (h *AlgorithmHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.Delete(id); err != nil {
		code := response.CodeNotFound
		if strings.Contains(err.Error(), "不允许删除") {
			code = response.CodeBadRequest // 状态闸门拦截，属业务校验错误而非 404
		}
		response.Error(c, code, err.Error())
		return
	}
	response.SuccessWithMessage(c, "deleted", nil)
}

// GetByID 根据 ID 查询算法
func (h *AlgorithmHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	algo, err := h.svc.GetByID(id)
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, algo)
}

// GetByCode 根据 Code 查询算法
func (h *AlgorithmHandler) GetByCode(c *gin.Context) {
	code := c.Param("code")
	algo, err := h.svc.GetByCode(code)
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, algo)
}

// List 查询算法列表
func (h *AlgorithmHandler) List(c *gin.Context) {
	var req request.ListAlgorithmRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}

	list, total := h.svc.List(req)
	response.Success(c, gin.H{
		"list":   list,
		"total":  total,
		"limit":  req.Limit,
		"offset": req.Offset,
	})
}
