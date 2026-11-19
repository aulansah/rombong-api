package category

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/rombong-api/internal/utils"
	"github.com/aulansah/rombong-api/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Create handles POST /categories (admin).
func (h *Handler) Create(c *fiber.Ctx) error {
	var req Request
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	cat, err := h.svc.Create(c.UserContext(), req)
	if err != nil {
		return err
	}
	return response.Created(c, "Category created", ToResponse(cat))
}

// List handles GET /categories.
func (h *Handler) List(c *fiber.Ctx) error {
	cats, err := h.svc.List(c.UserContext())
	if err != nil {
		return err
	}
	out := make([]Response, 0, len(cats))
	for i := range cats {
		out = append(out, ToResponse(&cats[i]))
	}
	return response.OK(c, "Categories retrieved", out)
}

// Get handles GET /categories/:id.
func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	cat, err := h.svc.Get(c.UserContext(), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Category retrieved", ToResponse(cat))
}

// Update handles PUT /categories/:id (admin).
func (h *Handler) Update(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	var req Request
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	cat, err := h.svc.Update(c.UserContext(), id, req)
	if err != nil {
		return err
	}
	return response.OK(c, "Category updated", ToResponse(cat))
}

// Delete handles DELETE /categories/:id (admin).
func (h *Handler) Delete(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.UserContext(), id); err != nil {
		return err
	}
	return response.NoContent(c)
}
