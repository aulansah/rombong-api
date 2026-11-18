// Package apperror defines the application error type shared by all layers.
//
// Repositories return the sentinel errors (ErrNotFound, ErrDuplicate),
// services translate them into *AppError values that carry an HTTP status and
// a stable machine-readable code, and the central Fiber error handler turns
// an *AppError into the standard JSON error envelope.
package apperror

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors returned by the repository layer.
var (
	ErrNotFound  = errors.New("record not found")
	ErrDuplicate = errors.New("duplicate record")
)

// Stable error codes exposed to API clients.
const (
	CodeBadRequest      = "BAD_REQUEST"
	CodeValidation      = "VALIDATION_ERROR"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeTooManyRequests = "TOO_MANY_REQUESTS"
	CodeInternal        = "INTERNAL_ERROR"
)

// FieldError describes a single invalid request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// AppError is an error that is safe to show to API clients.
// Err keeps the underlying cause for logging only; it is never serialized.
type AppError struct {
	Status  int
	Code    string
	Message string
	Details []FieldError
	Err     error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the underlying cause to errors.Is / errors.As.
func (e *AppError) Unwrap() error { return e.Err }

// New builds an AppError with an explicit status and code.
func New(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func BadRequest(message string) *AppError {
	return New(http.StatusBadRequest, CodeBadRequest, message)
}

func Validation(details []FieldError) *AppError {
	e := New(http.StatusUnprocessableEntity, CodeValidation, "Validation failed")
	e.Details = details
	return e
}

func Unauthorized(message string) *AppError {
	return New(http.StatusUnauthorized, CodeUnauthorized, message)
}

func Forbidden(message string) *AppError {
	return New(http.StatusForbidden, CodeForbidden, message)
}

func NotFound(message string) *AppError {
	return New(http.StatusNotFound, CodeNotFound, message)
}

func Conflict(message string) *AppError {
	return New(http.StatusConflict, CodeConflict, message)
}

func TooManyRequests(message string) *AppError {
	return New(http.StatusTooManyRequests, CodeTooManyRequests, message)
}

// Internal wraps an unexpected error. The client only ever sees a generic
// message; the cause is logged by the error handler.
func Internal(err error) *AppError {
	e := New(http.StatusInternalServerError, CodeInternal, "Internal server error")
	e.Err = err
	return e
}
