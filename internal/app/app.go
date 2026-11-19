// Package app is the composition root: it wires repositories, services and
// handlers together and builds the Fiber application.
//
// Dependency injection is done by hand with constructors. For a project of
// this size that is easier to read and debug than a DI framework, and the
// compiler checks every dependency.
package app

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/aulansah/rombong-api/docs"
	"github.com/aulansah/rombong-api/internal/cache"
	"github.com/aulansah/rombong-api/internal/config"
	"github.com/aulansah/rombong-api/internal/database"
	"github.com/aulansah/rombong-api/internal/middleware"
	"github.com/aulansah/rombong-api/internal/modules/auth"
	"github.com/aulansah/rombong-api/internal/modules/cart"
	"github.com/aulansah/rombong-api/internal/modules/category"
	"github.com/aulansah/rombong-api/internal/modules/order"
	"github.com/aulansah/rombong-api/internal/modules/payment"
	"github.com/aulansah/rombong-api/internal/modules/product"
	"github.com/aulansah/rombong-api/internal/modules/user"
	"github.com/aulansah/rombong-api/internal/routes"
	"github.com/aulansah/rombong-api/pkg/response"
	"github.com/aulansah/rombong-api/pkg/token"
)

// Deps are the infrastructure dependencies created in main (or in tests).
type Deps struct {
	Config *config.Config
	DB     *gorm.DB
	Cache  cache.Cache
	Log    *zap.Logger
}

// App bundles the HTTP application with the services that main needs
// directly (for example to seed the admin account).
type App struct {
	Fiber *fiber.App
	Auth  *auth.Service
}

// New builds the fully wired application.
func New(d Deps) *App {
	cfg := d.Config

	// Infrastructure helpers
	txManager := database.NewTxManager(d.DB)
	tokens := token.NewManager(cfg.JWT.Secret, cfg.JWT.AccessTTL, cfg.App.Name)

	// Repositories
	userRepo := user.NewRepository(d.DB)
	tokenRepo := auth.NewRepository(d.DB)
	categoryRepo := category.NewRepository(d.DB)
	productRepo := product.NewRepository(d.DB)
	cartRepo := cart.NewRepository(d.DB)
	orderRepo := order.NewRepository(d.DB)
	paymentRepo := payment.NewRepository(d.DB)

	// Services
	authSvc := auth.NewService(userRepo, tokenRepo, tokens, cfg.JWT.RefreshTTL, cfg.App.BcryptCost)
	userSvc := user.NewService(userRepo, tokenRepo, txManager, cfg.App.BcryptCost)
	categorySvc := category.NewService(categoryRepo)
	productSvc := product.NewService(productRepo, categorySvc, d.Cache, d.Log, cfg.Cache.ProductTTL, cfg.Cache.ProductListTTL)
	cartSvc := cart.NewService(cartRepo, productRepo)
	orderSvc := order.NewService(orderRepo, cartRepo, productRepo, productSvc, txManager)
	paymentSvc := payment.NewService(paymentRepo, orderRepo, txManager, cfg.Payment.Expiry)

	f := fiber.New(fiber.Config{
		AppName:               cfg.App.Name,
		ErrorHandler:          response.ErrorHandler(d.Log),
		DisableStartupMessage: true,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdleTimeout:           60 * time.Second,
		BodyLimit:             1 * 1024 * 1024,
	})

	// Global middleware. Order matters: the request ID must exist before
	// anything logs, and recover must wrap everything that can panic.
	f.Use(requestid.New())
	f.Use(middleware.RequestLogger(d.Log))
	f.Use(recover.New())
	f.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORS.AllowOrigins,
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization,Idempotency-Key,X-Request-ID",
	}))

	f.Get("/health", healthHandler(d))
	registerDocs(f)

	if cfg.RateLimit.Enabled {
		f.Use("/api", middleware.RateLimit(d.Cache, cfg.RateLimit.Max, cfg.RateLimit.Window, d.Log))
	}

	routes.Register(f, routes.Handlers{
		Auth:     auth.NewHandler(authSvc),
		User:     user.NewHandler(userSvc),
		Category: category.NewHandler(categorySvc),
		Product:  product.NewHandler(productSvc),
		Cart:     cart.NewHandler(cartSvc),
		Order:    order.NewHandler(orderSvc),
		Payment:  payment.NewHandler(paymentSvc),
	}, middleware.Auth(tokens))

	// Anything that did not match a route.
	f.Use(func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusNotFound, "Route not found")
	})

	return &App{Fiber: f, Auth: authSvc}
}

// healthHandler reports dependency status for Docker/Kubernetes probes.
// PostgreSQL down means the service is unhealthy (503). Redis down only
// degrades it, because the API keeps working without the cache.
func healthHandler(d Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
		defer cancel()

		dbStatus, redisStatus := "up", "up"
		status, code := "ok", fiber.StatusOK

		if sqlDB, err := d.DB.DB(); err != nil || sqlDB.PingContext(ctx) != nil {
			dbStatus, status, code = "down", "unavailable", fiber.StatusServiceUnavailable
		}
		if err := d.Cache.Ping(ctx); err != nil {
			redisStatus = "down"
			if status == "ok" {
				status = "degraded"
			}
		}

		return c.Status(code).JSON(fiber.Map{
			"status":   status,
			"database": dbStatus,
			"redis":    redisStatus,
		})
	}
}

// registerDocs serves the OpenAPI document and a Swagger UI page.
func registerDocs(f *fiber.App) {
	f.Get("/docs/openapi.yaml", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "application/yaml; charset=utf-8")
		return c.Send(docs.OpenAPI)
	})
	f.Get("/docs", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.SendString(strings.TrimSpace(docs.SwaggerUI))
	})
}
