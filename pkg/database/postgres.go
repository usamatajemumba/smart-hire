// Package database opens PostgreSQL connections (GORM) and runs SQL migrations.
package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the "pgx5://" driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/usamatajemumba/smart-hire/pkg/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config describes one PostgreSQL database.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	SSLMode  string

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DSN returns a libpq key/value connection string (used by GORM).
func (c Config) DSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode)
}

// MigrateURL returns a URL for golang-migrate's pgx/v5 driver.
func (c Config) MigrateURL() string {
	return fmt.Sprintf("pgx5://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Name, c.SSLMode)
}

// Open connects with GORM, configures the pool and verifies connectivity.
func Open(ctx context.Context, cfg Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Warn),
		TranslateError: true, // maps unique violations to gorm.ErrDuplicatedKey
		NowFunc:        func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	// Pool sizing matters under load: too small -> requests queue for a
	// connection; too big -> Postgres runs out of max_connections.
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

// Migrate applies all pending "up" migrations found in migrations (an embed.FS
// sub-tree containing NNNN_name.up.sql / NNNN_name.down.sql files).
func Migrate(cfg Config, migrations fs.FS, log *slog.Logger) error {
	src, err := iofs.New(migrations, ".")
	if err != nil {
		return fmt.Errorf("migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, cfg.MigrateURL())
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	v, dirty, _ := m.Version()
	log.Info("database migrated", "version", v, "dirty", dirty)
	return nil
}

// ConfigFromEnv reads DB_* environment variables; defaultName is used when
// DB_NAME is unset (each service owns its own database).
func ConfigFromEnv(defaultName string) Config {
	return Config{
		Host:            config.String("DB_HOST", "localhost"),
		Port:            config.Int("DB_PORT", 5432),
		User:            config.String("DB_USER", "smarthire"),
		Password:        config.String("DB_PASSWORD", "smarthire"),
		Name:            config.String("DB_NAME", defaultName),
		SSLMode:         config.String("DB_SSLMODE", "disable"),
		MaxOpenConns:    config.Int("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:    config.Int("DB_MAX_IDLE_CONNS", 10),
		ConnMaxLifetime: config.Duration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
	}
}
