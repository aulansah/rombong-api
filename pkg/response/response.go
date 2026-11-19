// Package response standardizes every JSON body the API returns.
package response

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/aulansah/rombong-api/pkg/apperror"
)

// Envelope is the success response format.
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	Meta    any    `json:"meta"`
}

// Meta carries pagination information for list endpoints.
type Meta struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
}

// NewMeta computes the page count for a list response.
func NewMeta(page, limit int, total int64) Meta {
	pages := 0
	if limit > 0 {
		pages = int((total + int64(limit) - 1) / int64(limit))
	}
	return Meta{Page: page, Limit: limit, TotalItems: total, TotalPages: pages}
}

// ErrorBody is the "error" object of an error response.
type ErrorBody struct {
	Code    string                `json:"code"`
	Details []apperror.FieldError `json:"details,omitempty"`
}

// ErrorEnvelope is the error response format.
type ErrorEnvelope struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

func OK(c *fiber.Ctx, message string, data any) error {
	return c.Status(fiber.StatusOK).JSON(Envelope{Success: true, Message: message, Data: data})
}

func Created(c *fiber.Ctx, message string, data any) error {
	return c.Status(fiber.StatusCreated).JSON(Envelope{Success: true, Message: message, Data: data})
}

func Paginated(c *fiber.Ctx, message string, data any, meta Meta) error {
	return c.Status(fiber.StatusOK).JSON(Envelope{Success: true, Message: message, Data: data, Meta: meta})
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// RequestID reads the correlation ID stored by the requestid middleware.
func RequestID(c *fiber.Ctx) string {
	if id, ok := c.Locals("requestid").(string); ok {
		return id
	}
	return ""
}

// ErrorHandler is the single place where errors become HTTP responses.
// Unknown errors are logged with their cause and reported as a generic 500 so
// that internal details (SQL, stack traces) never reach the client.
func ErrorHandler(log *zap.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		var appErr *apperror.AppError
		var fiberErr *fiber.Error

		switch {
		case errors.As(err, &appErr):
		case errors.As(err, &fiberErr):
			appErr = apperror.New(fiberErr.Code, codeForStatus(fiberErr.Code), fiberErr.Message)
		default:
			appErr = apperror.Internal(err)
		}

		if appErr.Status >= fiber.StatusInternalServerError {
			log.Error("request failed",
				zap.String("request_id", RequestID(c)),
				zap.String("method", c.Method()),
				zap.String("path", c.Path()),
				zap.Error(err),
			)
		}

		return c.Status(appErr.Status).JSON(ErrorEnvelope{
			Success:   false,
			Message:   appErr.Message,
			Error:     ErrorBody{Code: appErr.Code, Details: appErr.Details},
			RequestID: RequestID(c),
		})
	}
}

func codeForStatus(status int) string {
	switch status {
	case fiber.StatusBadRequest:
		return apperror.CodeBadRequest
	case fiber.StatusUnauthorized:
		return apperror.CodeUnauthorized
	case fiber.StatusForbidden:
		return apperror.CodeForbidden
	case fiber.StatusNotFound, fiber.StatusMethodNotAllowed:
		return apperror.CodeNotFound
	case fiber.StatusConflict:
		return apperror.CodeConflict
	case fiber.StatusUnprocessableEntity:
		return apperror.CodeValidation
	case fiber.StatusTooManyRequests:
		return apperror.CodeTooManyRequests
	default:
		if status >= fiber.StatusInternalServerError {
			return apperror.CodeInternal
		}
		return apperror.CodeBadRequest
	}
}
