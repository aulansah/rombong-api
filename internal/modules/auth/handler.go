package auth

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/go-ecommerce-api/internal/middleware"
	"github.com/aulansah/go-ecommerce-api/internal/modules/user"
	"github.com/aulansah/go-ecommerce-api/internal/utils"
	"github.com/aulansah/go-ecommerce-api/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register handles POST /auth/register.
func (h *Handler) Register(c *fiber.Ctx) error {
	var req RegisterRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	u, err := h.svc.Register(c.UserContext(), req)
	if err != nil {
		return err
	}
	return response.Created(c, "Registration successful", user.ToResponse(u))
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *fiber.Ctx) error {
	var req LoginRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	res, err := h.svc.Login(c.UserContext(), req)
	if err != nil {
		return err
	}
	return response.OK(c, "Login successful", res)
}

// Refresh handles POST /auth/refresh.
func (h *Handler) Refresh(c *fiber.Ctx) error {
	var req RefreshRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	res, err := h.svc.Refresh(c.UserContext(), req)
	if err != nil {
		return err
	}
	return response.OK(c, "Token refreshed", res)
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c *fiber.Ctx) error {
	var req RefreshRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	if err := h.svc.Logout(c.UserContext(), middleware.UserID(c), req); err != nil {
		return err
	}
	return response.OK(c, "Logout successful", nil)
}
