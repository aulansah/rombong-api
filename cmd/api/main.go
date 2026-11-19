// Command api starts the Rombong e-commerce REST API.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/aulansah/rombong-api/internal/app"
	"github.com/aulansah/rombong-api/internal/cache"
	"github.com/aulansah/rombong-api/internal/config"
	"github.com/aulansah/rombong-api/internal/database"
	"github.com/aulansah/rombong-api/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// run holds the real startup logic so that deferred cleanups execute before
// the process exits (os.Exit in main would skip them).
func run() error {
	migrateOnly := flag.Bool("migrate-only", false, "apply database migrations and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := logger.New(cfg.App.LogLevel, cfg.App.Env)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = log.Sync() }()

	if cfg.App.AutoMigrate || *migrateOnly {
		if err := database.Migrate(cfg.DB.DSN()); err != nil {
			return err
		}
		log.Info("database migrations applied")
		if *migrateOnly {
			return nil
		}
	}

	db, err := database.Connect(cfg.DB, log)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	// Redis is optional at startup: the API runs without it, just slower.
	redisCache := cache.NewRedis(cfg.Redis)
	defer redisCache.Close()
	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := redisCache.Ping(pingCtx); err != nil {
		log.Warn("redis unavailable, continuing without cache", zap.Error(err))
	} else {
		log.Info("redis connected", zap.String("addr", cfg.Redis.Addr))
	}
	cancel()

	application := app.New(app.Deps{Config: cfg, DB: db, Cache: redisCache, Log: log})

	if cfg.Admin.Email != "" && cfg.Admin.Password != "" {
		if err := application.Auth.EnsureAdmin(context.Background(), cfg.Admin.Name, cfg.Admin.Email, cfg.Admin.Password); err != nil {
			return fmt.Errorf("seed admin: %w", err)
		}
	}

	// Graceful shutdown: stop accepting connections on SIGINT/SIGTERM and
	// let in-flight requests finish, so a deploy does not cut off a checkout.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("server started", zap.String("port", cfg.App.Port), zap.String("env", cfg.App.Env))
		serverErr <- application.Fiber.Listen(":" + cfg.App.Port)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return fmt.Errorf("server stopped: %w", err)
	case sig := <-quit:
		log.Info("shutting down", zap.String("signal", sig.String()))
	}

	if err := application.Fiber.ShutdownWithTimeout(15 * time.Second); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("server stopped")
	return nil
}
