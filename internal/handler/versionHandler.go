package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/service"
	"awesomeProject/internal/runtime"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/response"
)

// VersionHandler 版本管理处理器
type VersionHandler struct {
	svc service.VersionService
	rt  *runtime.Manager // 算法运行时编排器：发布/归档时联动进程拉起与回收
}

// NewVersionHandler 创建版本处理器
func NewVersionHandler(svc service.VersionService, rt *runtime.Manager) *VersionHandler {
	return &VersionHandler{svc: svc, rt: rt}
}

// RegisterRoutes 注册路由
func (h *VersionHandler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/versions")
	{
		group.POST("", h.Create)
		group.PUT("/:id", h.Update)
		group.GET("/:id", h.GetByID)
		group.GET("", h.List)
		// 状态流转
		group.POST("/:id/publish", h.Publish)
		group.POST("/:id/archive", h.Archive)
		// 变更记录
		group.GET("/:id/changelog", h.ListChangeLog)
		// 版本文件（每个版本独立一套）
		group.POST("/:id/files", h.UploadFile)
		group.GET("/:id/files/url", h.GetFileURL)
		group.GET("/:id/files/batch", h.DownloadBatch)
		group.DELETE("/:id/files", h.DeleteFile)
	}
}

// operatorOf 从请求中取操作人，缺省为 system
func operatorOf(c *gin.Context) string {
	if op := c.Query("operator"); op != "" {
		return op
	}
	if op := c.PostForm("operator"); op != "" {
		return op
	}
	return "system"
}

// Create 创建版本（版本号留空则自动生成）
func (h *VersionHandler) Create(c *gin.Context) {
	var req request.CreateVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	version, err := h.svc.Create(req)
	if err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	response.SuccessWithMessage(c, "version created", version)
}

// Update 更新版本（仅草稿可改）
func (h *VersionHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var req request.UpdateVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	version, err := h.svc.Update(id, req)
	if err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	response.SuccessWithMessage(c, "version updated", version)
}

// GetByID 根据 ID 查询版本
func (h *VersionHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	version, err := h.svc.GetByID(id)
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, version)
}

// List 查询版本列表
func (h *VersionHandler) List(c *gin.Context) {
	var req request.ListVersionRequest
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

// Publish 发布版本
func (h *VersionHandler) Publish(c *gin.Context) {
	id := c.Param("id")
	version, err := h.svc.Publish(id, operatorOf(c))
	if err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	// 发布成功后触发算法进程拉起（同步等待就绪；Fail-Fast：拉不起来即视为发布失败）
	if h.rt != nil {
		if code, e := h.svc.AlgorithmCode(version.AlgorithmID); e != nil {
			logger.Warn("publish: 解析算法 code 失败", "version", id, "error", e)
		} else if e := h.rt.Launch(code); e != nil {
			logger.Error("publish: 拉起算法进程失败，回滚发布", "code", code, "error", e)
			if rerr := h.svc.RevertPublish(id, operatorOf(c)); rerr != nil {
				logger.Error("publish: 回滚发布状态失败", "version", id, "error", rerr)
			}
			response.Error(c, response.CodeBadGateway, "发布失败：算法进程拉起失败，已回滚为草稿。原因: "+e.Error())
			return
		}
	}
	response.SuccessWithMessage(c, "version published", version)
}

// Archive 归档版本（逻辑删除）
func (h *VersionHandler) Archive(c *gin.Context) {
	id := c.Param("id")
	version, err := h.svc.Archive(id, operatorOf(c))
	if err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	// 归档后：若该算法已无已发布版本，回收其进程（发布新版本时自动归档旧版本不在此触发，避免误杀）
	if h.rt != nil {
		if code, e := h.svc.AlgorithmCode(version.AlgorithmID); e != nil {
			logger.Warn("archive: 解析算法 code 失败", "version", id, "error", e)
		} else {
			h.rt.MaybeKill(code)
		}
	}
	response.SuccessWithMessage(c, "version archived", version)
}

// ListChangeLog 查询版本变更记录（按时间倒序分页）
func (h *VersionHandler) ListChangeLog(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Limit  int `form:"limit,default=20"`
		Offset int `form:"offset,default=0"`
	}
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	if req.Offset < 0 {
		req.Offset = 0
	}
	logs, total, err := h.svc.ListChangeLog(id, req.Limit, req.Offset)
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, gin.H{"list": logs, "total": total, "limit": req.Limit, "offset": req.Offset})
}

// UploadFile 上传版本文件（multipart 字段名为 file；支持多个 file 字段一次批量上传，
// 多文件时服务端合并为一条变更记录；单文件保持原行为）
func (h *VersionHandler) UploadFile(c *gin.Context) {
	id := c.Param("id")
	form, err := c.MultipartForm()
	if err != nil {
		response.Error(c, response.CodeBadRequest, "parse multipart form failed: "+err.Error())
		return
	}
	fileHeaders := form.File["file"]
	if len(fileHeaders) == 0 {
		response.Error(c, response.CodeBadRequest, "missing file field")
		return
	}
	paths := form.Value["path"]
	operator := operatorOf(c)

	// 批量：一次请求内的所有文件合并为一条变更记录
	if len(fileHeaders) > 1 {
		ins := make([]request.UploadFileInput, 0, len(fileHeaders))
		closers := make([]io.Closer, 0, len(fileHeaders))
		defer func() {
			for _, cl := range closers {
				_ = cl.Close()
			}
		}()
		for i, fh := range fileHeaders {
			f, err := fh.Open()
			if err != nil {
				response.Error(c, response.CodeInternalError, err.Error())
				return
			}
			closers = append(closers, f)
			rel := ""
			if i < len(paths) {
				rel = paths[i]
			}
			ins = append(ins, request.UploadFileInput{
				Reader:       f,
				Size:         fh.Size,
				Filename:     fh.Filename,
				ContentType:  fh.Header.Get("Content-Type"),
				RelativePath: rel,
			})
		}
		afs, err := h.svc.UploadFiles(c.Request.Context(), id, ins, operator)
		if err != nil {
			response.Error(c, response.CodeBadRequest, err.Error())
			return
		}
		response.Success(c, afs)
		return
	}

	// 单文件：保持原有行为
	fileHeader := fileHeaders[0]
	f, err := fileHeader.Open()
	if err != nil {
		response.Error(c, response.CodeInternalError, err.Error())
		return
	}
	defer f.Close()

	rel := c.PostForm("path")
	if len(paths) > 0 {
		rel = paths[0]
	}
	af, err := h.svc.UploadFile(c.Request.Context(), id, request.UploadFileInput{
		Reader:       f,
		Size:         fileHeader.Size,
		Filename:     fileHeader.Filename,
		ContentType:  fileHeader.Header.Get("Content-Type"),
		RelativePath: rel,
	}, operator)
	if err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, af)
}

// GetFileURL 获取版本文件下载地址（MinIO 预签名 / memory 代理路由）
func (h *VersionHandler) GetFileURL(c *gin.Context) {
	id := c.Param("id")
	key := c.Query("key")
	if key == "" {
		response.Error(c, response.CodeBadRequest, "missing key")
		return
	}
	url, err := h.svc.GetFileURL(c.Request.Context(), id, key, operatorOf(c))
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, gin.H{"url": url})
}

// DeleteFile 删除版本文件
func (h *VersionHandler) DeleteFile(c *gin.Context) {
	id := c.Param("id")
	key := c.Query("key")
	if key == "" {
		response.Error(c, response.CodeBadRequest, "missing key")
		return
	}
	if err := h.svc.DeleteFile(c.Request.Context(), id, key, operatorOf(c)); err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	response.SuccessWithMessage(c, "deleted", nil)
}

// DownloadBatch 批量下载版本文件（zip 打包，保留相对路径结构）
// 用法：GET /versions/:id/files/batch?key=<path1>&key=<path2>
// 返回原始 zip 字节流（application/zip），不走业务信封，便于前端直接保存文件
func (h *VersionHandler) DownloadBatch(c *gin.Context) {
	id := c.Param("id")
	keys := c.QueryArray("key")
	if len(keys) == 0 {
		response.Error(c, response.CodeBadRequest, "missing key(s)")
		return
	}
	rc, zipName, err := h.svc.BatchDownloadFiles(c.Request.Context(), id, keys, operatorOf(c))
	if err != nil {
		response.Error(c, response.CodeNotFound, err.Error())
		return
	}
	defer rc.Close()
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", zipName))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, rc); err != nil {
		logger.Warn("stream batch zip failed", "error", err)
	}
}
