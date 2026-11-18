package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/aulansah/go-ecommerce-api/internal/cache"
	"github.com/aulansah/go-ecommerce-api/pkg/response"
	"github.com/aulansah/go-ecommerce-api/pkg/token"
)

func newApp() *fiber.App {
	return fiber.New(fiber.Config{ErrorHandler: response.ErrorHandler(zap.NewNop())})
}

func call(t *testing.T, app *fiber.App, path, authorization string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	res, err := app.Test(req, -1)
	require.NoError(t, err)
	defer res.Body.Close()
	return res.StatusCode
}

func TestAuthAndRequireRole(t *testing.T) {
	tokens := token.NewManager("unit-test-secret-with-at-least-32-chars", time.Minute, "test")
	customer, err := tokens.Generate(uuid.New(), RoleCustomer)
	require.NoError(t, err)
	admin, err := tokens.Generate(uuid.New(), RoleAdmin)
	require.NoError(t, err)

	app := newApp()
	ok := func(c *fiber.Ctx) error {
		// The identity set by Auth must be readable by handlers.
		if UserID(c) == uuid.Nil {
			return fiber.ErrInternalServerError
		}
		return c.SendStatus(fiber.StatusOK)
	}
	app.Get("/private", Auth(tokens), ok)
	app.Get("/admin", Auth(tokens), RequireRole(RoleAdmin), ok)

	cases := []struct {
		name          string
		path          string
		authorization string
		want          int
	}{
		{"no header", "/private", "", http.StatusUnauthorized},
		{"wrong scheme", "/private", "Basic " + customer, http.StatusUnauthorized},
		{"empty bearer", "/private", "Bearer ", http.StatusUnauthorized},
		{"invalid token", "/private", "Bearer not-a-token", http.StatusUnauthorized},
		{"valid customer token", "/private", "Bearer " + customer, http.StatusOK},
		{"scheme is case-insensitive", "/private", "bearer " + customer, http.StatusOK},
		{"customer on admin route is forbidden", "/admin", "Bearer " + customer, http.StatusForbidden},
		{"admin on admin route", "/admin", "Bearer " + admin, http.StatusOK},
		{"admin route without token is unauthorized, not forbidden", "/admin", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, call(t, app, tc.path, tc.authorization))
		})
	}
}

func TestRateLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })

	app := newApp()
	app.Use(RateLimit(cache.NewRedisFromClient(client), 2, time.Minute, zap.NewNop()))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	assert.Equal(t, http.StatusOK, call(t, app, "/", ""))
	assert.Equal(t, http.StatusOK, call(t, app, "/", ""))
	assert.Equal(t, http.StatusTooManyRequests, call(t, app, "/", ""), "third request in the window is rejected")

	t.Run("window resets after the TTL", func(t *testing.T) {
		mr.FastForward(time.Minute + time.Second)
		assert.Equal(t, http.StatusOK, call(t, app, "/", ""))
	})

	t.Run("fails open when Redis is down", func(t *testing.T) {
		mr.Close()
		for i := 0; i < 5; i++ {
			assert.Equal(t, http.StatusOK, call(t, app, "/", ""))
		}
	})
}
