// Package middleware contains the cross-cutting HTTP concerns:
// authentication, authorization, request logging and rate limiting.
package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/aulansah/rombong-api/pkg/apperror"
	"github.com/aulansah/rombong-api/pkg/token"
)

const (
	RoleAdmin    = "admin"
	RoleCustomer = "customer"

	localUserID = "user_id"
	localRole   = "role"
)

// Auth verifies the Bearer access token and stores the caller's identity in
// the request context. Missing or invalid tokens produce 401.
func Auth(tokens *token.Manager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		scheme, value, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(value) == "" {
			return apperror.Unauthorized("Missing or malformed Authorization header")
		}

		claims, err := tokens.Parse(strings.TrimSpace(value))
		if err != nil {
			return apperror.Unauthorized("Invalid or expired token")
		}
		userID, err := claims.UserID()
		if err != nil {
			return apperror.Unauthorized("Invalid or expired token")
		}

		c.Locals(localUserID, userID)
		c.Locals(localRole, claims.Role)
		return c.Next()
	}
}

// RequireRole allows the request only when the authenticated user has one of
// the given roles. It must run after Auth. A valid token with the wrong role
// produces 403, which is different from 401 (no valid identity at all).
func RequireRole(roles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		current := Role(c)
		for _, r := range roles {
			if current == r {
				return c.Next()
			}
		}
		return apperror.Forbidden("You do not have permission to access this resource")
	}
}

// UserID returns the authenticated user's ID.
func UserID(c *fiber.Ctx) uuid.UUID {
	id, _ := c.Locals(localUserID).(uuid.UUID)
	return id
}

// Role returns the authenticated user's role.
func Role(c *fiber.Ctx) string {
	role, _ := c.Locals(localRole).(string)
	return role
}

// IsAdmin reports whether the authenticated user is an admin.
func IsAdmin(c *fiber.Ctx) bool { return Role(c) == RoleAdmin }
