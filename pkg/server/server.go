// Package server runs an http.Server with graceful shutdown.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Run serves h on addr until ctx is cancelled (e.g. SIGTERM from Docker), then
// stops accepting connections and waits up to shutdownTimeout for in-flight
// requests to finish. This is the first place context.Context shows up:
// cancellation flows from the OS signal down to the server.
func Run(ctx context.Context, addr string, h http.Handler, shutdownTimeout time.Duration, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second, // protects against slowloris
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// RegisterHealth adds liveness (/healthz) and readiness (/readyz) probes.
// Liveness: the process is up. Readiness: dependencies (DB) are reachable,
// so a load balancer / orchestrator may route traffic here.
func RegisterHealth(r *gin.Engine, db *gorm.DB) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		sqlDB, err := db.DB()
		if err == nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			err = sqlDB.PingContext(ctx)
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "postgres": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "postgres": "ok"})
	})
}
