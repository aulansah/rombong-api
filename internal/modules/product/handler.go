package product

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/aulansah/go-ecommerce-api/internal/utils"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
	"github.com/aulansah/go-ecommerce-api/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Create handles POST /products (admin).
func (h *Handler) Create(c *fiber.Ctx) error {
	var req SaveRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	p, err := h.svc.Create(c.UserContext(), req)
	if err != nil {
		return err
	}
	return response.Created(c, "Product created", p)
}

// List handles GET /products with search, filter, sorting and pagination.
func (h *Handler) List(c *fiber.Ctx) error {
	p := utils.ParsePagination(c)
	filter := ListFilter{
		Search: c.Query("search"),
		Sort:   c.Query("sort"),
		Order:  c.Query("order"),
		Page:   p.Page,
		Limit:  p.Limit,
	}
	if raw := c.Query("category_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return apperror.BadRequest("Invalid category_id: must be a UUID")
		}
		filter.CategoryID = &id
	}

	result, err := h.svc.List(c.UserContext(), filter)
	if err != nil {
		return err
	}
	return response.Paginated(c, "Products retrieved", result.Items, response.NewMeta(p.Page, p.Limit, result.Total))
}

// Get handles GET /products/:id.
func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	p, err := h.svc.Get(c.UserContext(), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Product retrieved", p)
}

// Update handles PUT /products/:id (admin).
func (h *Handler) Update(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	var req SaveRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	p, err := h.svc.Update(c.UserContext(), id, req)
	if err != nil {
		return err
	}
	return response.OK(c, "Product updated", p)
}

// UpdateStock handles PATCH /products/:id/stock (admin).
func (h *Handler) UpdateStock(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	var req StockRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	p, err := h.svc.UpdateStock(c.UserContext(), id, req)
	if err != nil {
		return err
	}
	return response.OK(c, "Stock updated", p)
}

// Delete handles DELETE /products/:id (admin).
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
