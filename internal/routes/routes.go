// Package routes maps URLs to handlers and attaches the access rules.
// Reading this file should be enough to know who can call what.
package routes

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/rombong-api/internal/middleware"
	"github.com/aulansah/rombong-api/internal/modules/auth"
	"github.com/aulansah/rombong-api/internal/modules/cart"
	"github.com/aulansah/rombong-api/internal/modules/category"
	"github.com/aulansah/rombong-api/internal/modules/order"
	"github.com/aulansah/rombong-api/internal/modules/payment"
	"github.com/aulansah/rombong-api/internal/modules/product"
	"github.com/aulansah/rombong-api/internal/modules/user"
)

// Handlers groups every module handler.
type Handlers struct {
	Auth     *auth.Handler
	User     *user.Handler
	Category *category.Handler
	Product  *product.Handler
	Cart     *cart.Handler
	Order    *order.Handler
	Payment  *payment.Handler
}

// Register mounts all /api/v1 routes.
// authn verifies the access token; admin additionally requires the admin role.
func Register(app *fiber.App, h Handlers, authn fiber.Handler) {
	admin := middleware.RequireRole(middleware.RoleAdmin)
	v1 := app.Group("/api/v1")

	// Authentication
	a := v1.Group("/auth")
	a.Post("/register", h.Auth.Register)
	a.Post("/login", h.Auth.Login)
	a.Post("/refresh", h.Auth.Refresh)
	a.Post("/logout", authn, h.Auth.Logout)

	// Users. "/me" routes are registered before "/:id" so "me" is never
	// parsed as an ID.
	u := v1.Group("/users", authn)
	u.Get("/me", h.User.GetProfile)
	u.Put("/me", h.User.UpdateProfile)
	u.Put("/me/password", h.User.ChangePassword)
	u.Get("/", admin, h.User.List)
	u.Get("/:id", admin, h.User.GetByID)

	// Categories: public reads, admin writes.
	c := v1.Group("/categories")
	c.Get("/", h.Category.List)
	c.Get("/:id", h.Category.Get)
	c.Post("/", authn, admin, h.Category.Create)
	c.Put("/:id", authn, admin, h.Category.Update)
	c.Delete("/:id", authn, admin, h.Category.Delete)

	// Products: public reads, admin writes.
	p := v1.Group("/products")
	p.Get("/", h.Product.List)
	p.Get("/:id", h.Product.Get)
	p.Post("/", authn, admin, h.Product.Create)
	p.Put("/:id", authn, admin, h.Product.Update)
	p.Patch("/:id/stock", authn, admin, h.Product.UpdateStock)
	p.Delete("/:id", authn, admin, h.Product.Delete)

	// Cart
	ct := v1.Group("/cart", authn)
	ct.Get("/", h.Cart.Get)
	ct.Delete("/", h.Cart.Clear)
	ct.Post("/items", h.Cart.AddItem)
	ct.Put("/items/:id", h.Cart.UpdateItem)
	ct.Delete("/items/:id", h.Cart.RemoveItem)

	// Orders. Ownership is enforced in the service layer.
	o := v1.Group("/orders", authn)
	o.Post("/", h.Order.Create)
	o.Get("/", h.Order.List)
	o.Get("/:id", h.Order.Get)
	o.Post("/:id/cancel", h.Order.Cancel)
	o.Patch("/:id/status", admin, h.Order.UpdateStatus)

	// Payment gateway webhook: authenticated by signature, not by login.
	v1.Post("/webhooks/midtrans", h.Payment.MidtransNotification)

	// Payments
	pay := v1.Group("/payments", authn)
	pay.Post("/", h.Payment.Create)
	pay.Get("/:id", h.Payment.Get)
	pay.Post("/:id/simulate-success", h.Payment.SimulateSuccess)
	pay.Post("/:id/simulate-failure", h.Payment.SimulateFailure)
}
