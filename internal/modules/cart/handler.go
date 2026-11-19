package cart

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/rombong-api/internal/middleware"
	"github.com/aulansah/rombong-api/internal/utils"
	"github.com/aulansah/rombong-api/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Get handles GET /cart.
func (h *Handler) Get(c *fiber.Ctx) error {
	cart, err := h.svc.Get(c.UserContext(), middleware.UserID(c))
	if err != nil {
		return err
	}
	return response.OK(c, "Cart retrieved", ToResponse(cart))
}

// AddItem handles POST /cart/items.
func (h *Handler) AddItem(c *fiber.Ctx) error {
	var req AddItemRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	cart, err := h.svc.AddItem(c.UserContext(), middleware.UserID(c), req)
	if err != nil {
		return err
	}
	return response.OK(c, "Item added to cart", ToResponse(cart))
}

// UpdateItem handles PUT /cart/items/:id.
func (h *Handler) UpdateItem(c *fiber.Ctx) error {
	itemID, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	var req UpdateItemRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	cart, err := h.svc.UpdateItem(c.UserContext(), middleware.UserID(c), itemID, req)
	if err != nil {
		return err
	}
	return response.OK(c, "Cart item updated", ToResponse(cart))
}

// RemoveItem handles DELETE /cart/items/:id.
func (h *Handler) RemoveItem(c *fiber.Ctx) error {
	itemID, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	cart, err := h.svc.RemoveItem(c.UserContext(), middleware.UserID(c), itemID)
	if err != nil {
		return err
	}
	return response.OK(c, "Cart item removed", ToResponse(cart))
}

// Clear handles DELETE /cart.
func (h *Handler) Clear(c *fiber.Ctx) error {
	if err := h.svc.Clear(c.UserContext(), middleware.UserID(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}
