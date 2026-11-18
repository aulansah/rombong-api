package payment

import (
	"github.com/gofiber/fiber/v2"

	"github.com/aulansah/go-ecommerce-api/internal/middleware"
	"github.com/aulansah/go-ecommerce-api/internal/utils"
	"github.com/aulansah/go-ecommerce-api/pkg/response"
)

// HeaderIdempotencyKey is the request header that makes POST /payments
// safe to retry.
const HeaderIdempotencyKey = "Idempotency-Key"

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func caller(c *fiber.Ctx) Caller {
	return Caller{UserID: middleware.UserID(c), IsAdmin: middleware.IsAdmin(c)}
}

// Create handles POST /payments. It answers 201 for a new payment and 200
// when the same Idempotency-Key is replayed.
func (h *Handler) Create(c *fiber.Ctx) error {
	var req CreateRequest
	if err := utils.ParseBody(c, &req); err != nil {
		return err
	}
	p, created, err := h.svc.Create(c.UserContext(), caller(c), c.Get(HeaderIdempotencyKey), req)
	if err != nil {
		return err
	}
	if created {
		return response.Created(c, "Payment created", ToResponse(p))
	}
	return response.OK(c, "Payment already exists for this Idempotency-Key", ToResponse(p))
}

// Get handles GET /payments/:id.
func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	p, err := h.svc.Get(c.UserContext(), caller(c), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Payment retrieved", ToResponse(p))
}

// SimulateSuccess handles POST /payments/:id/simulate-success.
func (h *Handler) SimulateSuccess(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	p, err := h.svc.SimulateSuccess(c.UserContext(), caller(c), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Payment successful", ToResponse(p))
}

// SimulateFailure handles POST /payments/:id/simulate-failure.
func (h *Handler) SimulateFailure(c *fiber.Ctx) error {
	id, err := utils.ParseUUIDParam(c, "id")
	if err != nil {
		return err
	}
	p, err := h.svc.SimulateFailure(c.UserContext(), caller(c), id)
	if err != nil {
		return err
	}
	return response.OK(c, "Payment failed", ToResponse(p))
}
