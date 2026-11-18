package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/app"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/response"
	"awesomeProject/pkg/utils"
)

// VersionHandler handles version management.
type VersionHandler struct {
	svc          service.VersionService
	pub          app.VersionApp
	maxFileBytes int64
}

// NewVersionHandler creates the version handler. maxFileBytes <= 0 disables the per-file limit.
func NewVersionHandler(svc service.VersionService, pub app.VersionApp, maxFileBytes int64) *VersionHandler {
	return &VersionHandler{svc: svc, pub: pub, maxFileBytes: maxFileBytes}
}

// RegisterRoutes registers the routes.
func (h *VersionHandler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/versions")
	{
		group.POST("", h.Create)
		group.PUT("/:id", h.Update)
		group.GET("/:id", h.GetByID)
		group.GET("", h.List)
		// Status transitions: publish/archive need runtime coordination and are orchestrated by the application layer.
		group.POST("/:id/publish", h.Publish)
		group.POST("/:id/archive", h.Archive)
		// Change logs.
		group.GET("/:id/changelog", h.ListChangeLog)
		// Version files (each version owns an independent set).
		group.POST("/:id/files", h.UploadFile)
		group.GET("/:id/files/url", h.GetFileURL)
		group.GET("/:id/files/batch", h.DownloadBatch)
		group.DELETE("/:id/files", h.DeleteFile)
	}
}

// Create creates a version; the version number is auto-generated when empty.
func (h *VersionHandler) Create(c *gin.Context) {
	var req request.CreateVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	// The audit actor always comes from the authenticated context, never from the request body.
	req.Operator = operatorOf(c)
	version, err := h.svc.Create(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "version created", version)
}

// Update updates a version (drafts only).
func (h *VersionHandler) Update(c *gin.Context) {
	var req request.UpdateVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	// The audit actor always comes from the authenticated context, never from the request body.
	req.Operator = operatorOf(c)
	version, err := h.svc.Update(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "version updated", version)
}

// GetByID returns a version by ID.
func (h *VersionHandler) GetByID(c *gin.Context) {
	version, err := h.svc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, version)
}

// List returns a page of versions.
func (h *VersionHandler) List(c *gin.Context) {
	var req request.ListVersionRequest
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

// Publish publishes a version and launches the algorithm; a failed launch rolls back the publish state.
func (h *VersionHandler) Publish(c *gin.Context) {
	version, err := h.pub.Publish(c.Request.Context(), c.Param("id"), operatorOf(c))
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "version published", version)
}

// Archive archives a version (logical delete).
func (h *VersionHandler) Archive(c *gin.Context) {
	version, err := h.pub.Archive(c.Request.Context(), c.Param("id"), operatorOf(c))
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "version archived", version)
}

// ListChangeLog returns change logs, newest first, with pagination.
func (h *VersionHandler) ListChangeLog(c *gin.Context) {
	var req struct {
		Limit  int `form:"limit,default=20"`
		Offset int `form:"offset,default=0"`
	}
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, response.CodeBadRequest, err.Error())
		return
	}
	req.Limit, req.Offset = utils.NormalizePaging(req.Limit, req.Offset)
	logs, total, err := h.svc.ListChangeLog(c.Request.Context(), c.Param("id"), req.Limit, req.Offset)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, gin.H{"list": logs, "total": total, "limit": req.Limit, "offset": req.Offset})
}

// UploadFile uploads version files from the multipart "file" field. Multiple file fields are uploaded
// in one request and merged into a single change log entry; a single file keeps the original behavior.
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
	if h.maxFileBytes > 0 {
		for _, fh := range fileHeaders {
			if fh.Size > h.maxFileBytes {
				response.Error(c, response.CodeBadRequest,
					fmt.Sprintf("file %s exceeds the %d byte limit", fh.Filename, h.maxFileBytes))
				return
			}
		}
	}
	paths := form.Value["path"]
	operator := operatorOf(c)
	ctx := c.Request.Context()

	// Batch: merge all files in one request into a single change log entry.
	if len(fileHeaders) > 1 {
		ins := make([]request.UploadFileInput, 0, len(fileHeaders))
		closers := make([]io.Closer, 0, len(fileHeaders))
		defer func() {
			for _, cl := range closers {
				_ = cl.Close()
			}
		}()
		for i, fh := range fileHeaders {
			f, oerr := fh.Open()
			if oerr != nil {
				internalError(c, oerr)
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
		afs, uerr := h.svc.UploadFiles(ctx, id, ins, operator)
		if uerr != nil {
			respondError(c, uerr)
			return
		}
		response.Success(c, afs)
		return
	}

	// Single file: keep the original behavior.
	fileHeader := fileHeaders[0]
	f, err := fileHeader.Open()
	if err != nil {
		internalError(c, err)
		return
	}
	defer f.Close()

	rel := c.PostForm("path")
	if len(paths) > 0 {
		rel = paths[0]
	}
	af, err := h.svc.UploadFile(ctx, id, request.UploadFileInput{
		Reader:       f,
		Size:         fileHeader.Size,
		Filename:     fileHeader.Filename,
		ContentType:  fileHeader.Header.Get("Content-Type"),
		RelativePath: rel,
	}, operator)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, af)
}

// GetFileURL returns a download URL for a version file (read-only).
func (h *VersionHandler) GetFileURL(c *gin.Context) {
	key := c.Query("key")
	if key == "" {
		response.Error(c, response.CodeBadRequest, "missing key")
		return
	}
	url, err := h.svc.GetFileURL(c.Request.Context(), c.Param("id"), key)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, gin.H{"url": url})
}

// DeleteFile deletes a version file.
func (h *VersionHandler) DeleteFile(c *gin.Context) {
	key := c.Query("key")
	if key == "" {
		response.Error(c, response.CodeBadRequest, "missing key")
		return
	}
	if err := h.svc.DeleteFile(c.Request.Context(), c.Param("id"), key, operatorOf(c)); err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithMessage(c, "deleted", nil)
}

// DownloadBatch streams the selected version files as a zip archive, preserving relative paths.
// Usage: GET /versions/:id/files/batch?key=<path1>&key=<path2>
// Returns the raw zip stream (application/zip) instead of the business envelope so browsers can save it directly.
func (h *VersionHandler) DownloadBatch(c *gin.Context) {
	keys := c.QueryArray("key")
	if len(keys) == 0 {
		response.Error(c, response.CodeBadRequest, "missing key(s)")
		return
	}
	rc, zipName, err := h.svc.BatchDownloadFiles(c.Request.Context(), c.Param("id"), keys)
	if err != nil {
		respondError(c, err)
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
