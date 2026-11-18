package utils

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
)

const (
	DefaultPage  = 1
	DefaultLimit = 20
	MaxLimit     = 100
)

// Pagination is the normalized page/limit pair of a list request.
type Pagination struct {
	Page  int
	Limit int
}

// Offset returns the SQL OFFSET for the page.
func (p Pagination) Offset() int { return (p.Page - 1) * p.Limit }

// ParsePagination reads ?page=&limit= and clamps them to safe bounds, so a
// client can never request an unbounded result set.
func ParsePagination(c *fiber.Ctx) Pagination {
	return NewPagination(c.QueryInt("page", DefaultPage), c.QueryInt("limit", DefaultLimit))
}

// NewPagination clamps arbitrary values to the allowed range.
func NewPagination(page, limit int) Pagination {
	if page < 1 {
		page = DefaultPage
	}
	if limit < 1 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return Pagination{Page: page, Limit: limit}
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts "Sepatu Lari Pria!" into "sepatu-lari-pria".
func Slugify(s string) string {
	slug := nonSlugChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(slug, "-")
}

// RandomHex returns n random bytes as a hex string (2n characters).
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
