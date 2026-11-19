// Package utils holds small HTTP helpers shared by the handlers.
package utils

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/aulansah/rombong-api/pkg/apperror"
)

// validate is safe for concurrent use and caches struct metadata, so a
// single instance is shared by the whole application.
var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New()

	// Report the JSON field name ("email") instead of the Go one ("Email").
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return f.Name
		}
		return name
	})

	// "password": at least 8 characters with upper, lower and digit.
	_ = v.RegisterValidation("password", func(fl validator.FieldLevel) bool {
		return IsStrongPassword(fl.Field().String())
	})
	return v
}

// IsStrongPassword enforces the password policy.
// bcrypt ignores everything after 72 bytes, so longer inputs are rejected
// rather than silently truncated.
func IsStrongPassword(p string) bool {
	if len(p) < 8 || len(p) > 72 {
		return false
	}
	var upper, lower, digit bool
	for _, r := range p {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return upper && lower && digit
}

// ParseBody decodes the JSON body into dst and validates it.
func ParseBody(c *fiber.Ctx, dst any) error {
	if err := c.BodyParser(dst); err != nil {
		return apperror.BadRequest("Invalid request body")
	}
	return ValidateStruct(dst)
}

// ValidateStruct runs the struct tags and converts failures to a 422.
func ValidateStruct(v any) error {
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperror.Internal(err)
	}
	details := make([]apperror.FieldError, 0, len(verrs))
	for _, fe := range verrs {
		details = append(details, apperror.FieldError{Field: fe.Field(), Message: messageFor(fe)})
	}
	return apperror.Validation(details)
}

func messageFor(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "gt":
		return fmt.Sprintf("must be greater than %s", fe.Param())
	case "gte":
		return fmt.Sprintf("must be greater than or equal to %s", fe.Param())
	case "lte":
		return fmt.Sprintf("must be less than or equal to %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	case "nefield":
		return fmt.Sprintf("must be different from %s", fe.Param())
	case "password":
		return "must be 8-72 characters and contain uppercase, lowercase and a digit"
	default:
		return "is invalid"
	}
}

// ParseUUIDParam reads a UUID path parameter.
func ParseUUIDParam(c *fiber.Ctx, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(name))
	if err != nil {
		return uuid.Nil, apperror.BadRequest(fmt.Sprintf("Invalid %s: must be a UUID", name))
	}
	return id, nil
}
