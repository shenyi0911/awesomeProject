package handler

import (
	"io"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/app"
	"awesomeProject/pkg/response"
)

// invokeNamePattern restricts code/method path segments to safe URL characters (dots allowed for versioned method names).
var invokeNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// validInvokeName rejects empty values, unsafe characters and traversal sequences.
func validInvokeName(v string) bool {
	return invokeNamePattern.MatchString(v) && !strings.Contains(v, "..")
}

// CalculateHandler exposes the algorithm execution layer over HTTP.
// Routes: POST /{code}/{method}, GET /{code}/metadata, GET /{code}/contract.
// Session handling, release gating, forwarding and response decoration are orchestrated by app.InvokeApp.
type CalculateHandler struct {
	inv app.InvokeApp
}

// NewCalculateHandler creates the execution-layer handler.
func NewCalculateHandler(inv app.InvokeApp) *CalculateHandler {
	return &CalculateHandler{inv: inv}
}

// RegisterRoutes registers the routes (rg is the /api/v1 group).
func (h *CalculateHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/:code/:method", h.InvokeGeneric)
	rg.GET("/:code/metadata", h.Metadata) // Algorithm self-description (callers discover method names and units from it).
	rg.GET("/:code/contract", h.Contract) // Platform-side structured method contract.
}

// InvokeGeneric forwards /api/v1/{code}/{method} to Dapr invoke {code}/method/{method}.
func (h *CalculateHandler) InvokeGeneric(c *gin.Context) {
	code := c.Param("code")
	method := c.Param("method")
	if code == "" {
		response.Error(c, response.CodeBadRequest, "missing algorithm code; usage: POST /api/v1/{code}/{method}")
		return
	}
	if method == "" {
		response.Error(c, response.CodeBadRequest, "missing method name; usage: POST /api/v1/{code}/{method} (method names are defined by each algorithm)")
		return
	}
	if method == "metadata" {
		response.Error(c, response.CodeBadRequest, "use GET /api/v1/{code}/metadata to fetch metadata")
		return
	}
	if !validInvokeName(code) || !validInvokeName(method) {
		response.Error(c, response.CodeBadRequest, "invalid algorithm code or method name")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Error(c, response.CodeBadRequest, "read request body failed (possibly exceeds the size limit): "+err.Error())
		return
	}

	res, err := h.inv.Invoke(c.Request.Context(), code, method, body, c.GetHeader("X-Session-Id"))
	if err != nil {
		respondError(c, err)
		return
	}
	if res.SessionID != "" {
		c.Header("X-Session-Id", res.SessionID)
	}
	c.Data(res.Status, "application/json; charset=utf-8", res.Body)
}

// Metadata proxies the algorithm's self-description endpoint.
func (h *CalculateHandler) Metadata(c *gin.Context) {
	body, status, err := h.inv.Metadata(c.Request.Context(), c.Param("code"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

// Contract returns the platform-side structured method contract (algorithm-agnostic, no process needed).
func (h *CalculateHandler) Contract(c *gin.Context) {
	methods, err := h.inv.Contract(c.Request.Context(), c.Param("code"))
	if err != nil {
		respondError(c, err)
		return
	}
	capabilities := make([]string, 0, len(methods))
	for _, m := range methods {
		capabilities = append(capabilities, m.Name)
	}
	response.Success(c, gin.H{
		"capabilities": capabilities,
		"methods":      methods,
	})
}
