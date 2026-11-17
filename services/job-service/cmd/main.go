// Command job-service runs the SmartHire job management API.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/usamatajemumba/smart-hire/pkg/config"
	"github.com/usamatajemumba/smart-hire/pkg/database"
	"github.com/usamatajemumba/smart-hire/pkg/logger"
	"github.com/usamatajemumba/smart-hire/pkg/middleware"
	"github.com/usamatajemumba/smart-hire/pkg/server"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/handler"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/migrations"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/repository"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/service"
)

const serviceName = "job-service"

func main() {
	addr := config.String("HTTP_ADDR", ":8081")
	server.MaybeRunHealthcheck(addr) // `/app healthcheck` for Docker; exits if used

	log := logger.New(serviceName, config.String("LOG_LEVEL", "info"))
	slog.SetDefault(log)

	if err := run(addr, log); err != nil {
		log.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
	log.Info("service stopped cleanly")
}

func run(addr string, log *slog.Logger) error {
	// ctx is cancelled on Ctrl+C / `docker stop` (SIGTERM) -> graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbCfg := database.ConfigFromEnv("smarthire_jobs")
	if err := database.Migrate(dbCfg, migrations.FS, log); err != nil {
		return err
	}
	db, err := database.Open(ctx, dbCfg)
	if err != nil {
		return err
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	// Wiring (manual dependency injection): repo -> service -> handler.
	svc := service.New(repository.New(db))
	h := handler.New(svc)

	gin.SetMode(config.String("GIN_MODE", gin.ReleaseMode))
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Logger(log), middleware.Recovery(log))
	server.RegisterHealth(r, db)
	h.Register(r.Group("/api/v1"))

	return server.Run(ctx, addr, r, config.Duration("SHUTDOWN_TIMEOUT", 15*time.Second), log)
}
