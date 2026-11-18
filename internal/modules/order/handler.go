package order

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/go-ecommerce-api/internal/middleware"
	"github.com/aulansah/go-ecommerce-api/internal/utils"
	"github.com/aulansah/go-ecommerce-api/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Create handles POST /orders. The request has no body: the order is built
// entirely from the server-side cart.
func (h *Handler) Create(c *fiber.Ctx) error {
	o, err := h.svc.Create(c.UserContext(), middleware.UserID(c))
	if err != nil {
		return err
	}
	return response.Created(c, "Order created", ToResponse(o))
}

// List handles GET /orders.
func (h *Handler) List(c *fiber.Ctx) error {
	p := utils.ParsePagination(c)
	orders, total, err := h.svc.List(c.UserContext(), middleware.UserID(c), middleware.IsAdmin(c), p.Offset(), p.Limit)
	if err != nil {
		return err
	}
	return response.Paginated(c, "Orders retrieved", ToResponses(orders), response.NewMeta(p.Page, p.Limit, total))
}

// Get handles GET /orders/:id.
func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.Get(c.UserContext(), middleware.UserID(c), middleware.IsAdmin(c), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Order retrieved", ToResponse(o))
}

// Cancel handles POST /orders/:id/cancel.
func (h *Handler) Cancel(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.Cancel(c.UserContext(), middleware.UserID(c), middleware.IsAdmin(c), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Order cancelled", ToResponse(o))
}

// UpdateStatus handles PATCH /orders/:id/status (admin).
func (h *Handler) UpdateStatus(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	var req UpdateStatusRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	o, err := h.svc.UpdateStatus(c.UserContext(), middleware.UserID(c), id, req)
	if err != nil {
		return err
	}
	return response.OK(c, "Order status updated", ToResponse(o))
}
