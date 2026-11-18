package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	platformmetrics "awesomeProject/internal/metrics"
)

// Metrics records per-route HTTP latency and status for Prometheus.
func Metrics(m *platformmetrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if m == nil {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		if route == "/metrics" {
			return
		}
		m.ObserveHTTP(c.Request.Method, route, c.Writer.Status(), time.Since(start))
	}
}
