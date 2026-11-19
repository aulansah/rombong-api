package user

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/rombong-api/internal/middleware"
	"github.com/aulansah/rombong-api/internal/utils"
	"github.com/aulansah/rombong-api/pkg/response"
)

// Handler translates HTTP requests into service calls. It contains no
// business rules.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// GetProfile handles GET /users/me.
func (h *Handler) GetProfile(c *fiber.Ctx) error {
	u, err := h.svc.GetByID(c.UserContext(), middleware.UserID(c))
	if err != nil {
		return err
	}
	return response.OK(c, "Profile retrieved", ToResponse(u))
}

// UpdateProfile handles PUT /users/me.
func (h *Handler) UpdateProfile(c *fiber.Ctx) error {
	var req UpdateProfileRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	u, err := h.svc.UpdateProfile(c.UserContext(), middleware.UserID(c), req)
	if err != nil {
		return err
	}
	return response.OK(c, "Profile updated", ToResponse(u))
}

// ChangePassword handles PUT /users/me/password.
func (h *Handler) ChangePassword(c *fiber.Ctx) error {
	var req ChangePasswordRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	if err := h.svc.ChangePassword(c.UserContext(), middleware.UserID(c), req); err != nil {
		return err
	}
	return response.OK(c, "Password changed, please log in again", nil)
}

// GetByID handles GET /users/:id (admin).
func (h *Handler) GetByID(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	u, err := h.svc.GetByID(c.UserContext(), id)
	if err != nil {
		return err
	}
	return response.OK(c, "User retrieved", ToResponse(u))
}

// List handles GET /users (admin).
func (h *Handler) List(c *fiber.Ctx) error {
	p := utils.ParsePagination(c)
	users, total, err := h.svc.List(c.UserContext(), p.Offset(), p.Limit)
	if err != nil {
		return err
	}
	return response.Paginated(c, "Users retrieved", ToResponses(users), response.NewMeta(p.Page, p.Limit, total))
}
