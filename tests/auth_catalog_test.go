//go:build integration

package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthFlow(t *testing.T) {
	resetDB(t)
	creds := map[string]string{"name": "Auldy", "email": "Auldy@Example.com", "password": userPassword}

	t.Run("register", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/register", "", creds)
		require.Equal(t, http.StatusCreated, res.Status, res.Raw)
		assert.Equal(t, true, res.Body["success"])
		assert.Equal(t, "auldy@example.com", res.Data()["email"])
		assert.Equal(t, "customer", res.Data()["role"])
		assert.NotContains(t, strings.ToLower(res.Raw), "password", "no password or hash in the response")
	})

	t.Run("register cannot choose a role", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/register", "", map[string]string{
			"name": "Mallory", "email": "mallory@example.com", "password": userPassword, "role": "admin",
		})
		require.Equal(t, http.StatusCreated, res.Status)
		assert.Equal(t, "customer", res.Data()["role"])
	})

	t.Run("duplicate email", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/register", "", creds)
		assert.Equal(t, http.StatusConflict, res.Status)
		assert.Equal(t, "CONFLICT", res.ErrorCode())
	})

	t.Run("validation errors use the standard error format", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/register", "", map[string]string{"name": "A", "email": "nope", "password": "weak"})
		assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
		assert.Equal(t, false, res.Body["success"])
		assert.Equal(t, "VALIDATION_ERROR", res.ErrorCode())
		assert.Len(t, res.Body["error"].(map[string]any)["details"], 3)
		assert.NotEmpty(t, res.Body["request_id"])
	})

	t.Run("malformed JSON", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/login", "", "not-an-object")
		assert.Contains(t, []int{http.StatusBadRequest, http.StatusUnprocessableEntity}, res.Status)
	})

	t.Run("login with wrong password", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/login", "", map[string]string{"email": "auldy@example.com", "password": "Wrong1234"})
		assert.Equal(t, http.StatusUnauthorized, res.Status)
	})

	access, refresh := loginAs(t, "auldy@example.com", userPassword)

	t.Run("profile requires a token", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodGet, "/users/me", "", nil).Status)
		assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodGet, "/users/me", "garbage", nil).Status)

		res := call(t, http.MethodGet, "/users/me", access, nil)
		require.Equal(t, http.StatusOK, res.Status)
		assert.Equal(t, "auldy@example.com", res.Data()["email"])
	})

	t.Run("update profile", func(t *testing.T) {
		res := call(t, http.MethodPut, "/users/me", access, map[string]string{"name": "Auldy Ansya"})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Equal(t, "Auldy Ansya", res.Data()["name"])
	})

	t.Run("refresh rotates the token and detects reuse", func(t *testing.T) {
		res := call(t, http.MethodPost, "/auth/refresh", "", map[string]string{"refresh_token": refresh})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		rotated := res.Data()["refresh_token"].(string)
		assert.NotEqual(t, refresh, rotated)

		// Replaying the old token is treated as theft: all sessions die.
		res = call(t, http.MethodPost, "/auth/refresh", "", map[string]string{"refresh_token": refresh})
		assert.Equal(t, http.StatusUnauthorized, res.Status)
		res = call(t, http.MethodPost, "/auth/refresh", "", map[string]string{"refresh_token": rotated})
		assert.Equal(t, http.StatusUnauthorized, res.Status)
	})

	t.Run("logout revokes the refresh token", func(t *testing.T) {
		access, refresh := loginAs(t, "auldy@example.com", userPassword)
		res := call(t, http.MethodPost, "/auth/logout", access, map[string]string{"refresh_token": refresh})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)

		res = call(t, http.MethodPost, "/auth/refresh", "", map[string]string{"refresh_token": refresh})
		assert.Equal(t, http.StatusUnauthorized, res.Status)
	})

	t.Run("change password signs out other sessions", func(t *testing.T) {
		access, refresh := loginAs(t, "auldy@example.com", userPassword)

		res := call(t, http.MethodPut, "/users/me/password", access, map[string]string{
			"current_password": "Wrong1234", "new_password": "NewSecret456",
		})
		assert.Equal(t, http.StatusBadRequest, res.Status)

		res = call(t, http.MethodPut, "/users/me/password", access, map[string]string{
			"current_password": userPassword, "new_password": "NewSecret456",
		})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)

		res = call(t, http.MethodPost, "/auth/refresh", "", map[string]string{"refresh_token": refresh})
		assert.Equal(t, http.StatusUnauthorized, res.Status, "old sessions are revoked")

		res = call(t, http.MethodPost, "/auth/login", "", map[string]string{"email": "auldy@example.com", "password": userPassword})
		assert.Equal(t, http.StatusUnauthorized, res.Status, "old password no longer works")
		loginAs(t, "auldy@example.com", "NewSecret456")
	})
}

func TestAuthorization(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Elektronik")
	productID := createProduct(t, admin, categoryID, "Headphone", 250000, 5)

	product := map[string]any{"name": "Speaker", "price": 100000, "stock": 1, "category_id": categoryID}
	adminOnly := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/users", nil},
		{http.MethodGet, "/users/" + uuid.NewString(), nil},
		{http.MethodPost, "/categories", map[string]string{"name": "Baru"}},
		{http.MethodPut, "/categories/" + categoryID, map[string]string{"name": "Baru"}},
		{http.MethodDelete, "/categories/" + categoryID, nil},
		{http.MethodPost, "/products", product},
		{http.MethodPut, "/products/" + productID, product},
		{http.MethodPatch, "/products/" + productID + "/stock", map[string]int{"stock": 9}},
		{http.MethodDelete, "/products/" + productID, nil},
		{http.MethodPatch, "/orders/" + uuid.NewString() + "/status", map[string]string{"status": "SHIPPED"}},
	}
	for _, ep := range adminOnly {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			res := call(t, ep.method, ep.path, "", ep.body)
			assert.Equal(t, http.StatusUnauthorized, res.Status, "anonymous")
			assert.Equal(t, "UNAUTHORIZED", res.ErrorCode())

			res = call(t, ep.method, ep.path, customer, ep.body)
			assert.Equal(t, http.StatusForbidden, res.Status, "customer")
			assert.Equal(t, "FORBIDDEN", res.ErrorCode())
		})
	}

	t.Run("admin can list users with pagination meta", func(t *testing.T) {
		res := call(t, http.MethodGet, "/users?page=1&limit=1", admin, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Len(t, res.List(), 1)
		meta := res.Body["meta"].(map[string]any)
		assert.EqualValues(t, 2, meta["total_items"])
		assert.EqualValues(t, 2, meta["total_pages"])
	})

	t.Run("unknown route uses the standard error format", func(t *testing.T) {
		res := call(t, http.MethodGet, "/nope", "", nil)
		assert.Equal(t, http.StatusNotFound, res.Status)
		assert.Equal(t, "NOT_FOUND", res.ErrorCode())
	})
}

func TestCategoryAndProductCatalog(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)

	minuman := createCategory(t, admin, "Minuman")
	makanan := createCategory(t, admin, "Makanan")

	t.Run("category rules", func(t *testing.T) {
		res := call(t, http.MethodPost, "/categories", admin, map[string]string{"name": "minuman"})
		assert.Equal(t, http.StatusConflict, res.Status, "same slug")

		res = call(t, http.MethodGet, "/categories", "", nil)
		require.Equal(t, http.StatusOK, res.Status)
		assert.Len(t, res.List(), 2)

		res = call(t, http.MethodGet, "/categories/"+uuid.NewString(), "", nil)
		assert.Equal(t, http.StatusNotFound, res.Status)

		res = call(t, http.MethodGet, "/categories/not-a-uuid", "", nil)
		assert.Equal(t, http.StatusBadRequest, res.Status)
	})

	kopi := createProduct(t, admin, minuman, "Kopi Arabika", 85000, 10)
	createProduct(t, admin, minuman, "Teh Hijau", 30000, 20)
	createProduct(t, admin, makanan, "Keripik Singkong", 15000, 0)

	t.Run("product validation", func(t *testing.T) {
		res := call(t, http.MethodPost, "/products", admin, map[string]any{
			"name": "X", "price": -5, "stock": -1, "category_id": "abc",
		})
		assert.Equal(t, http.StatusUnprocessableEntity, res.Status)

		res = call(t, http.MethodPost, "/products", admin, map[string]any{
			"name": "Produk Hantu", "price": 1000, "stock": 1, "category_id": uuid.NewString(),
		})
		assert.Equal(t, http.StatusBadRequest, res.Status, "category must exist")
	})

	t.Run("list, search, filter, sort, paginate", func(t *testing.T) {
		res := call(t, http.MethodGet, "/products", "", nil)
		require.Equal(t, http.StatusOK, res.Status)
		assert.Len(t, res.List(), 3)

		res = call(t, http.MethodGet, "/products?search=KOPI", "", nil)
		require.Len(t, res.List(), 1)
		assert.Equal(t, "Kopi Arabika", res.List()[0].(map[string]any)["name"])

		res = call(t, http.MethodGet, "/products?search=%25", "", nil)
		assert.Empty(t, res.List(), "LIKE wildcards in the search term are escaped")

		res = call(t, http.MethodGet, "/products?category_id="+minuman, "", nil)
		assert.Len(t, res.List(), 2)

		res = call(t, http.MethodGet, "/products?sort=price&order=asc", "", nil)
		assert.Equal(t, "Keripik Singkong", res.List()[0].(map[string]any)["name"])

		res = call(t, http.MethodGet, "/products?sort=price&order=desc&limit=2&page=2", "", nil)
		require.Len(t, res.List(), 1)
		assert.Equal(t, "Keripik Singkong", res.List()[0].(map[string]any)["name"])
		meta := res.Body["meta"].(map[string]any)
		assert.EqualValues(t, 3, meta["total_items"])
		assert.EqualValues(t, 2, meta["total_pages"])

		res = call(t, http.MethodGet, "/products?sort=password_hash;drop", "", nil)
		assert.Equal(t, http.StatusOK, res.Status, "unknown sort falls back to the default")
	})

	t.Run("cached detail is invalidated by writes", func(t *testing.T) {
		res := call(t, http.MethodGet, "/products/"+kopi, "", nil)
		require.Equal(t, http.StatusOK, res.Status)
		assert.EqualValues(t, 85000, res.Data()["price"])

		res = call(t, http.MethodPut, "/products/"+kopi, admin, map[string]any{
			"name": "Kopi Arabika Gayo", "price": 95000, "stock": 10, "category_id": minuman,
		})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)

		res = call(t, http.MethodGet, "/products/"+kopi, "", nil)
		assert.EqualValues(t, 95000, res.Data()["price"])
		assert.Equal(t, "Kopi Arabika Gayo", res.Data()["name"])

		res = call(t, http.MethodPatch, "/products/"+kopi+"/stock", admin, map[string]int{"stock": 0})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.EqualValues(t, 0, res.Data()["stock"], "stock 0 is a valid value, not a missing field")

		res = call(t, http.MethodGet, "/products?search=gayo", "", nil)
		require.Len(t, res.List(), 1, "list cache was invalidated too")
	})

	t.Run("category with products cannot be deleted", func(t *testing.T) {
		res := call(t, http.MethodDelete, "/categories/"+minuman, admin, nil)
		assert.Equal(t, http.StatusConflict, res.Status)
	})

	t.Run("soft delete hides the product", func(t *testing.T) {
		res := call(t, http.MethodDelete, "/products/"+kopi, admin, nil)
		require.Equal(t, http.StatusNoContent, res.Status)

		assert.Equal(t, http.StatusNotFound, call(t, http.MethodGet, "/products/"+kopi, "", nil).Status)
		assert.Len(t, call(t, http.MethodGet, "/products", "", nil).List(), 2)

		var count int64
		require.NoError(t, testDB.Raw("SELECT count(*) FROM products WHERE id = ? AND deleted_at IS NOT NULL", kopi).Scan(&count).Error)
		assert.EqualValues(t, 1, count, "the row still exists in the database")
	})
}
