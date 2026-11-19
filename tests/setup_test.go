//go:build integration

// Package tests contains integration tests that run the real application
// against a real PostgreSQL database (Redis is replaced by miniredis).
//
// They are excluded from the default "go test ./..." run by the "integration"
// build tag. Run them with:
//
//	make test-integration
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/aulansah/rombong-api/internal/app"
	"github.com/aulansah/rombong-api/internal/cache"
	"github.com/aulansah/rombong-api/internal/config"
	"github.com/aulansah/rombong-api/internal/database"
)

const (
	adminEmail    = "admin@example.com"
	adminPassword = "Admin12345"
	userPassword  = "Secret123"
)

var (
	testApp *app.App
	testDB  *gorm.DB
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestMain(m *testing.M) {
	cfg := &config.Config{
		App: config.AppConfig{Name: "rombong-api-test", Env: "test", BcryptCost: bcrypt.MinCost},
		DB: config.DBConfig{
			Host:         env("TEST_DB_HOST", "localhost"),
			Port:         env("TEST_DB_PORT", "5432"),
			User:         env("TEST_DB_USER", "postgres"),
			Password:     env("TEST_DB_PASSWORD", "postgres"),
			Name:         env("TEST_DB_NAME", "ecommerce_test"),
			SSLMode:      "disable",
			MaxOpenConns: 30,
			MaxIdleConns: 10,
		},
		JWT: config.JWTConfig{
			Secret:     "integration-test-secret-with-32-chars!!",
			AccessTTL:  15 * time.Minute,
			RefreshTTL: time.Hour,
		},
		CORS:      config.CORSConfig{AllowOrigins: "http://localhost:3000"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Cache:     config.CacheConfig{ProductTTL: time.Minute, ProductListTTL: time.Minute},
		Payment:   config.PaymentConfig{Expiry: 30 * time.Minute},
	}

	if err := database.Migrate(cfg.DB.DSN()); err != nil {
		fmt.Fprintln(os.Stderr, "migrate test database:", err)
		os.Exit(1)
	}
	db, err := database.Connect(cfg.DB, zap.NewNop())
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect test database:", err)
		os.Exit(1)
	}
	testDB = db

	mr, err := miniredis.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start miniredis:", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	testApp = app.New(app.Deps{Config: cfg, DB: db, Cache: cache.NewRedisFromClient(redisClient), Log: zap.NewNop()})

	code := m.Run()

	_ = redisClient.Close()
	mr.Close()
	os.Exit(code)
}

// resetDB gives every test a clean database with only the admin account.
func resetDB(t *testing.T) {
	t.Helper()
	err := testDB.Exec(`TRUNCATE payments, order_items, orders, cart_items, carts,
		products, categories, refresh_tokens, users RESTART IDENTITY CASCADE`).Error
	require.NoError(t, err)
	require.NoError(t, testApp.Auth.EnsureAdmin(t.Context(), "Admin", adminEmail, adminPassword))
}

// result is a decoded API response.
type result struct {
	Status int
	Body   map[string]any
	Raw    string
}

// Data returns the "data" object of a success response.
func (r result) Data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)
	return d
}

// List returns the "data" array of a list response.
func (r result) List() []any {
	l, _ := r.Body["data"].([]any)
	return l
}

// ErrorCode returns error.code of an error response.
func (r result) ErrorCode() string {
	e, _ := r.Body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// call performs an HTTP request against the in-memory application.
func call(t testing.TB, method, path, token string, body any, headers ...string) result {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	res, err := testApp.Fiber.Test(req, -1)
	require.NoError(t, err)
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	out := result{Status: res.StatusCode, Raw: string(raw)}
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out.Body), "response is not JSON: %s", raw)
	}
	return out
}

// loginAs returns an access token and refresh token.
func loginAs(t testing.TB, email, password string) (access, refresh string) {
	t.Helper()
	res := call(t, http.MethodPost, "/auth/login", "", map[string]string{"email": email, "password": password})
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	tokens := res.Data()["tokens"].(map[string]any)
	return tokens["access_token"].(string), tokens["refresh_token"].(string)
}

func adminToken(t testing.TB) string {
	t.Helper()
	access, _ := loginAs(t, adminEmail, adminPassword)
	return access
}

// newCustomer registers a fresh customer and returns their access token.
func newCustomer(t testing.TB) string {
	t.Helper()
	email := fmt.Sprintf("user-%s@example.com", uuid.NewString()[:8])
	res := call(t, http.MethodPost, "/auth/register", "", map[string]string{
		"name": "Test User", "email": email, "password": userPassword,
	})
	require.Equal(t, http.StatusCreated, res.Status, res.Raw)
	access, _ := loginAs(t, email, userPassword)
	return access
}

func createCategory(t testing.TB, admin, name string) string {
	t.Helper()
	res := call(t, http.MethodPost, "/categories", admin, map[string]string{"name": name})
	require.Equal(t, http.StatusCreated, res.Status, res.Raw)
	return res.Data()["id"].(string)
}

func createProduct(t testing.TB, admin, categoryID, name string, price int64, stock int) string {
	t.Helper()
	res := call(t, http.MethodPost, "/products", admin, map[string]any{
		"name": name, "description": "test product", "price": price, "stock": stock, "category_id": categoryID,
	})
	require.Equal(t, http.StatusCreated, res.Status, res.Raw)
	return res.Data()["id"].(string)
}

func addToCart(t testing.TB, token, productID string, quantity int) result {
	t.Helper()
	return call(t, http.MethodPost, "/cart/items", token, map[string]any{"product_id": productID, "quantity": quantity})
}

func productStock(t testing.TB, productID string) int {
	t.Helper()
	var stock int
	require.NoError(t, testDB.Raw("SELECT stock FROM products WHERE id = ?", productID).Scan(&stock).Error)
	return stock
}

// placeOrder adds one product to the cart and checks out.
func placeOrder(t testing.TB, token, productID string, quantity int) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, addToCart(t, token, productID, quantity).Status)
	res := call(t, http.MethodPost, "/orders", token, nil)
	require.Equal(t, http.StatusCreated, res.Status, res.Raw)
	return res.Data()
}

func createPayment(t testing.TB, token, orderID, key string) result {
	t.Helper()
	return call(t, http.MethodPost, "/payments", token, map[string]string{"order_id": orderID}, "Idempotency-Key", key)
}
