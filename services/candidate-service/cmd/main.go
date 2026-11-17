// Command candidate-service runs the SmartHire candidate management API.
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
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/handler"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/migrations"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/repository"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/service"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/storage"
)

const serviceName = "candidate-service"

func main() {
	addr := config.String("HTTP_ADDR", ":8082")
	server.MaybeRunHealthcheck(addr)

	log := logger.New(serviceName, config.String("LOG_LEVEL", "info"))
	slog.SetDefault(log)

	if err := run(addr, log); err != nil {
		log.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
	log.Info("service stopped cleanly")
}

func run(addr string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbCfg := database.ConfigFromEnv("smarthire_candidates")
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

	blobs, err := storage.NewLocal(config.String("RESUME_STORAGE_DIR", "./data/resumes"))
	if err != nil {
		return err
	}

	svc := service.New(repository.New(db), blobs)
	h := handler.New(svc)

	gin.SetMode(config.String("GIN_MODE", gin.ReleaseMode))
	r := gin.New()
	r.MaxMultipartMemory = 1 << 20 // larger uploads spill to temp files, not RAM
	r.Use(middleware.RequestID(), middleware.Logger(log), middleware.Recovery(log))
	server.RegisterHealth(r, db)
	h.Register(r.Group("/api/v1"))

	return server.Run(ctx, addr, r, config.Duration("SHUTDOWN_TIMEOUT", 15*time.Second), log)
}
