// Package middleware holds Gin middleware shared by every service.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/httpx"
)

const RequestIDHeader = "X-Request-ID"

// RequestID reuses an incoming X-Request-ID or generates one, stores it in the
// context and echoes it back. It lets you correlate logs across services
// (Week 3 replaces/extends this with OpenTelemetry trace IDs).
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(httpx.RequestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// Logger writes one structured log line per request.
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		log.LogAttrs(c.Request.Context(), level, "http request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.String("route", c.FullPath()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
			slog.String("request_id", c.GetString(httpx.RequestIDKey)),
		)
	}
}

// Recovery converts panics into a 500 JSON error instead of killing the connection.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		log.Error("panic recovered", "panic", recovered, "request_id", c.GetString(httpx.RequestIDKey))
		c.AbortWithStatusJSON(http.StatusInternalServerError, httpx.ErrorBody{Error: httpx.ErrorPayload{
			Code: "INTERNAL_ERROR", Message: "internal server error", RequestID: c.GetString(httpx.RequestIDKey),
		}})
	})
}
