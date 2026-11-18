// Package middleware provides common HTTP middleware: request ID, access logging,
// CORS, body size limiting, optional authentication, and panic recovery.
package middleware

import (
	"crypto/subtle"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/reqctx"
	"awesomeProject/pkg/response"
	"awesomeProject/pkg/utils"
)

// RequestID assigns or propagates X-Request-Id so logs can be correlated across services.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = utils.NewID("req_")
		}
		c.Header("X-Request-Id", id)
		c.Request = c.Request.WithContext(reqctx.WithID(c.Request.Context(), id))
		c.Next()
	}
}

// AccessLog records one structured access log entry per request.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("http_access",
			"request_id", reqctx.IDFrom(c.Request.Context()),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// CORS allows cross-origin requests. An empty allowlist or "*" allows every origin; otherwise the
// request Origin must be in the list and is echoed back.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowAll := len(allowedOrigins) == 0
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			allowAll = true
			continue
		}
		allowed[o] = struct{}{}
	}
	return func(c *gin.Context) {
		h := c.Writer.Header()
		origin := c.GetHeader("Origin")
		if allowAll {
			h.Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			if _, ok := allowed[origin]; ok {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
		}
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Session-Id, X-Request-Id")
		h.Set("Access-Control-Expose-Headers", "X-Session-Id, X-Request-Id")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// BodyLimit limits request body size by Content-Type: multipart uses uploadLimit, others use jsonLimit.
func BodyLimit(jsonLimit, uploadLimit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		limit := jsonLimit
		if strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/") {
			limit = uploadLimit
		}
		if limit > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// BearerAuth enforces a static bearer token; health/readiness checks and preflight requests are allowed.
// The optional operator is stored as the trusted audit actor for authenticated requests.
func BearerAuth(token, operator string) gin.HandlerFunc {
	expected := []byte("Bearer " + token)
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions || c.Request.URL.Path == "/health" || c.Request.URL.Path == "/ready" || c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}
		got := []byte(c.GetHeader("Authorization"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			response.Error(c, response.CodeUnauthorized, "unauthorized")
			c.Abort()
			return
		}
		if operator != "" {
			c.Request = c.Request.WithContext(reqctx.WithOperator(c.Request.Context(), operator))
		}
		c.Next()
	}
}

// Recovery catches panics, logs the stack trace, and returns a unified 500 response.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		logger.Error("http_panic",
			"request_id", reqctx.IDFrom(c.Request.Context()),
			"path", c.Request.URL.Path,
			"panic", recovered,
			"stack", string(debug.Stack()),
		)
		response.Error(c, response.CodeInternalError, "internal server error")
		c.Abort()
	})
}
