//go:build integration

package tests

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aulansah/rombong-api/internal/database"
	"github.com/aulansah/rombong-api/internal/modules/product"
)

func TestCartRules(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Minuman")
	kopi := createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 5)
	teh := createProduct(t, admin, categoryID, "Teh Hijau", 30000, 10)
	habis := createProduct(t, admin, categoryID, "Susu Habis", 20000, 0)

	t.Run("empty cart is created on first read", func(t *testing.T) {
		res := call(t, http.MethodGet, "/cart", customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Empty(t, res.Data()["items"])
		assert.EqualValues(t, 0, res.Data()["total_amount"])
	})

	t.Run("rejects invalid input and unavailable products", func(t *testing.T) {
		assert.Equal(t, http.StatusUnprocessableEntity, addToCart(t, customer, kopi, 0).Status, "quantity 0")
		assert.Equal(t, http.StatusUnprocessableEntity, addToCart(t, customer, kopi, -2).Status, "negative quantity")
		assert.Equal(t, http.StatusNotFound, addToCart(t, customer, uuid.NewString(), 1).Status, "unknown product")
		assert.Equal(t, http.StatusConflict, addToCart(t, customer, habis, 1).Status, "out of stock")
		assert.Equal(t, http.StatusConflict, addToCart(t, customer, kopi, 6).Status, "more than stock")
	})

	t.Run("adding the same product accumulates and totals are recalculated", func(t *testing.T) {
		require.Equal(t, http.StatusOK, addToCart(t, customer, kopi, 2).Status)
		require.Equal(t, http.StatusOK, addToCart(t, customer, teh, 3).Status)
		res := addToCart(t, customer, kopi, 1)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)

		items := res.Data()["items"].([]any)
		require.Len(t, items, 2, "same product stays on one line")
		first := items[0].(map[string]any)
		assert.EqualValues(t, 3, first["quantity"])
		assert.EqualValues(t, 255000, first["subtotal"])
		assert.EqualValues(t, 6, res.Data()["total_items"])
		assert.EqualValues(t, 255000+90000, res.Data()["total_amount"])

		assert.Equal(t, http.StatusConflict, addToCart(t, customer, kopi, 3).Status, "3 + 3 exceeds stock 5")
	})

	cartItems := call(t, http.MethodGet, "/cart", customer, nil).Data()["items"].([]any)
	kopiItemID := cartItems[0].(map[string]any)["id"].(string)

	t.Run("update quantity", func(t *testing.T) {
		res := call(t, http.MethodPut, "/cart/items/"+kopiItemID, customer, map[string]int{"quantity": 5})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.EqualValues(t, 425000+90000, res.Data()["total_amount"])

		res = call(t, http.MethodPut, "/cart/items/"+kopiItemID, customer, map[string]int{"quantity": 6})
		assert.Equal(t, http.StatusConflict, res.Status)
	})

	t.Run("cart reflects the current price", func(t *testing.T) {
		res := call(t, http.MethodPut, "/products/"+teh, admin, map[string]any{
			"name": "Teh Hijau", "price": 35000, "stock": 10, "category_id": categoryID,
		})
		require.Equal(t, http.StatusOK, res.Status)

		res = call(t, http.MethodGet, "/cart", customer, nil)
		assert.EqualValues(t, 425000+105000, res.Data()["total_amount"])
	})

	t.Run("another user cannot touch the item", func(t *testing.T) {
		other := newCustomer(t)
		res := call(t, http.MethodPut, "/cart/items/"+kopiItemID, other, map[string]int{"quantity": 1})
		assert.Equal(t, http.StatusNotFound, res.Status)
		res = call(t, http.MethodDelete, "/cart/items/"+kopiItemID, other, nil)
		assert.Equal(t, http.StatusNotFound, res.Status)
	})

	t.Run("remove item and clear cart", func(t *testing.T) {
		res := call(t, http.MethodDelete, "/cart/items/"+kopiItemID, customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Len(t, res.Data()["items"], 1)

		require.Equal(t, http.StatusNoContent, call(t, http.MethodDelete, "/cart", customer, nil).Status)
		assert.Empty(t, call(t, http.MethodGet, "/cart", customer, nil).Data()["items"])
	})
}

func TestOrderCheckout(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Minuman")
	kopi := createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 5)
	teh := createProduct(t, admin, categoryID, "Teh Hijau", 30000, 10)

	t.Run("empty cart cannot be checked out", func(t *testing.T) {
		res := call(t, http.MethodPost, "/orders", customer, nil)
		assert.Equal(t, http.StatusBadRequest, res.Status)
	})

	require.Equal(t, http.StatusOK, addToCart(t, customer, kopi, 2).Status)
	require.Equal(t, http.StatusOK, addToCart(t, customer, teh, 1).Status)

	// The client tries to dictate prices; the body must be ignored.
	res := call(t, http.MethodPost, "/orders", customer, map[string]any{
		"total_amount": 1, "items": []map[string]any{{"product_id": kopi, "unit_price": 1}},
	})
	require.Equal(t, http.StatusCreated, res.Status, res.Raw)
	order := res.Data()
	orderID := order["id"].(string)

	t.Run("order uses server-side prices", func(t *testing.T) {
		assert.Equal(t, "PENDING", order["status"])
		assert.EqualValues(t, 2*85000+30000, order["total_amount"])
		assert.Regexp(t, `^ORD-\d{8}-[0-9A-F]{8}$`, order["order_number"])
		assert.Len(t, order["items"], 2)
	})

	t.Run("stock is reduced and the cart is emptied", func(t *testing.T) {
		assert.Equal(t, 3, productStock(t, kopi))
		assert.Equal(t, 9, productStock(t, teh))
		assert.Empty(t, call(t, http.MethodGet, "/cart", customer, nil).Data()["items"])

		res := call(t, http.MethodGet, "/products/"+kopi, "", nil)
		assert.EqualValues(t, 3, res.Data()["stock"], "product cache was invalidated after checkout")
	})

	t.Run("order items keep the name and price snapshot", func(t *testing.T) {
		res := call(t, http.MethodPut, "/products/"+kopi, admin, map[string]any{
			"name": "Kopi Rebranded", "price": 999999, "stock": 3, "category_id": categoryID,
		})
		require.Equal(t, http.StatusOK, res.Status)
		require.Equal(t, http.StatusNoContent, call(t, http.MethodDelete, "/products/"+teh, admin, nil).Status)

		res = call(t, http.MethodGet, "/orders/"+orderID, customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.EqualValues(t, 200000, res.Data()["total_amount"])

		names := map[string]float64{}
		for _, raw := range res.Data()["items"].([]any) {
			it := raw.(map[string]any)
			names[it["product_name"].(string)] = it["unit_price"].(float64)
		}
		assert.Equal(t, map[string]float64{"Kopi Arabika": 85000, "Teh Hijau": 30000}, names)
	})

	t.Run("order access is restricted to the owner and admins", func(t *testing.T) {
		other := newCustomer(t)
		assert.Equal(t, http.StatusNotFound, call(t, http.MethodGet, "/orders/"+orderID, other, nil).Status)
		assert.Equal(t, http.StatusNotFound, call(t, http.MethodPost, "/orders/"+orderID+"/cancel", other, nil).Status)
		assert.Empty(t, call(t, http.MethodGet, "/orders", other, nil).List())

		assert.Equal(t, http.StatusOK, call(t, http.MethodGet, "/orders/"+orderID, admin, nil).Status)
		assert.Len(t, call(t, http.MethodGet, "/orders", admin, nil).List(), 1)
		assert.Len(t, call(t, http.MethodGet, "/orders", customer, nil).List(), 1)
	})

	t.Run("checkout fails as a whole when one item is out of stock", func(t *testing.T) {
		buyer := newCustomer(t)
		susu := createProduct(t, admin, categoryID, "Susu Segar", 20000, 2)
		require.Equal(t, http.StatusOK, addToCart(t, buyer, kopi, 1).Status)
		require.Equal(t, http.StatusOK, addToCart(t, buyer, susu, 2).Status)

		// Stock drops after the item was added to the cart.
		res := call(t, http.MethodPatch, "/products/"+susu+"/stock", admin, map[string]int{"stock": 1})
		require.Equal(t, http.StatusOK, res.Status)

		res = call(t, http.MethodPost, "/orders", buyer, nil)
		assert.Equal(t, http.StatusConflict, res.Status, res.Raw)

		assert.Equal(t, 3, productStock(t, kopi), "kopi reservation was rolled back")
		assert.Equal(t, 1, productStock(t, susu))
		assert.Len(t, call(t, http.MethodGet, "/cart", buyer, nil).Data()["items"], 2, "cart is untouched")
		assert.Empty(t, call(t, http.MethodGet, "/orders", buyer, nil).List())
	})
}

// TestConcurrentCheckoutNeverOversells is the core correctness test: 20
// customers try to buy the last 5 units at the same moment.
func TestConcurrentCheckoutNeverOversells(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	categoryID := createCategory(t, admin, "Flash Sale")
	const stock, buyers = 5, 20
	productID := createProduct(t, admin, categoryID, "Limited Sneakers", 500000, stock)

	tokens := make([]string, buyers)
	for i := range tokens {
		tokens[i] = newCustomer(t)
		require.Equal(t, http.StatusOK, addToCart(t, tokens[i], productID, 1).Status)
	}

	var created, conflict, other atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, tok := range tokens {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			<-start
			switch call(t, http.MethodPost, "/orders", tok, nil).Status {
			case http.StatusCreated:
				created.Add(1)
			case http.StatusConflict:
				conflict.Add(1)
			default:
				other.Add(1)
			}
		}(tok)
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, stock, created.Load(), "exactly as many orders as units in stock")
	assert.EqualValues(t, buyers-stock, conflict.Load(), "everyone else gets 409")
	assert.EqualValues(t, 0, other.Load())
	assert.Equal(t, 0, productStock(t, productID), "stock ends at zero, never negative")

	var sold int64
	require.NoError(t, testDB.Raw("SELECT COALESCE(SUM(quantity), 0) FROM order_items WHERE product_id = ?", productID).Scan(&sold).Error)
	assert.EqualValues(t, stock, sold)
}

// One user double-clicking "checkout" must not get two orders from one cart.
func TestConcurrentCheckoutOfSameCartCreatesOneOrder(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Minuman")
	productID := createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 100)
	require.Equal(t, http.StatusOK, addToCart(t, customer, productID, 2).Status)

	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if call(t, http.MethodPost, "/orders", customer, nil).Status == http.StatusCreated {
				created.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, created.Load())
	assert.Equal(t, 98, productStock(t, productID))
}

func TestOrderCancelAndStatusFlow(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Minuman")
	productID := createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 10)

	t.Run("cancel restores stock exactly once", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 4)["id"].(string)
		require.Equal(t, 6, productStock(t, productID))

		res := call(t, http.MethodPost, "/orders/"+orderID+"/cancel", customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Equal(t, "CANCELLED", res.Data()["status"])
		assert.Equal(t, 10, productStock(t, productID))

		res = call(t, http.MethodPost, "/orders/"+orderID+"/cancel", customer, nil)
		assert.Equal(t, http.StatusConflict, res.Status)
		assert.Equal(t, 10, productStock(t, productID), "second cancel must not add stock again")
	})

	t.Run("concurrent cancels restore stock once", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 3)["id"].(string)
		require.Equal(t, 7, productStock(t, productID))

		var ok atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if call(t, http.MethodPost, "/orders/"+orderID+"/cancel", customer, nil).Status == http.StatusOK {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()

		assert.EqualValues(t, 1, ok.Load())
		assert.Equal(t, 10, productStock(t, productID))
	})

	t.Run("admin status flow follows the state machine", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		setStatus := func(status string) result {
			return call(t, http.MethodPatch, "/orders/"+orderID+"/status", admin, map[string]string{"status": status})
		}

		assert.Equal(t, http.StatusConflict, setStatus("SHIPPED").Status, "PENDING cannot jump to SHIPPED")
		assert.Equal(t, http.StatusUnprocessableEntity, setStatus("PAID").Status, "PAID is set by payments only")
		assert.Equal(t, http.StatusUnprocessableEntity, setStatus("WHATEVER").Status)

		pay := createPayment(t, customer, orderID, uuid.NewString())
		require.Equal(t, http.StatusCreated, pay.Status, pay.Raw)
		res := call(t, http.MethodPost, "/payments/"+pay.Data()["id"].(string)+"/simulate-success", customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)

		assert.Equal(t, http.StatusConflict, setStatus("COMPLETED").Status, "PAID cannot jump to COMPLETED")
		assert.Equal(t, http.StatusConflict, setStatus("CANCELLED").Status, "paid orders are not cancellable")
		for _, next := range []string{"PROCESSING", "SHIPPED", "COMPLETED"} {
			res := setStatus(next)
			require.Equal(t, http.StatusOK, res.Status, res.Raw)
			assert.Equal(t, next, res.Data()["status"])
		}
		assert.Equal(t, http.StatusConflict, setStatus("SHIPPED").Status, "COMPLETED is terminal")

		res = call(t, http.MethodPost, "/orders/"+orderID+"/cancel", customer, nil)
		assert.Equal(t, http.StatusConflict, res.Status, "customer cannot cancel a completed order")
	})

	t.Run("admin can cancel a pending order", func(t *testing.T) {
		before := productStock(t, productID)
		orderID := placeOrder(t, customer, productID, 2)["id"].(string)
		res := call(t, http.MethodPatch, "/orders/"+orderID+"/status", admin, map[string]string{"status": "CANCELLED"})
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Equal(t, before, productStock(t, productID))
	})
}

// Repository-level tests for the two primitives everything else relies on.
func TestRepository_StockAndTransactions(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	categoryID := createCategory(t, admin, "Minuman")
	id := uuid.MustParse(createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 3))

	repo := product.NewRepository(testDB)
	tx := database.NewTxManager(testDB)
	ctx := context.Background()

	t.Run("DecreaseStock returns the updated row", func(t *testing.T) {
		p, err := repo.DecreaseStock(ctx, id, 2)
		require.NoError(t, err)
		assert.Equal(t, 1, p.Stock)
		assert.Equal(t, "Kopi Arabika", p.Name)
		assert.EqualValues(t, 85000, p.Price)
	})

	t.Run("DecreaseStock refuses to go below zero", func(t *testing.T) {
		_, err := repo.DecreaseStock(ctx, id, 2)
		assert.ErrorIs(t, err, product.ErrInsufficientStock)
		assert.Equal(t, 1, productStock(t, id.String()))

		_, err = repo.DecreaseStock(ctx, uuid.New(), 1)
		assert.ErrorIs(t, err, product.ErrInsufficientStock, "unknown product")
	})

	t.Run("a failed transaction rolls back repository writes", func(t *testing.T) {
		boom := errors.New("boom")
		err := tx.WithinTx(ctx, func(ctx context.Context) error {
			if _, err := repo.DecreaseStock(ctx, id, 1); err != nil {
				return err
			}
			return boom
		})
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, 1, productStock(t, id.String()), "the decrement was rolled back")
	})

	t.Run("the database itself rejects negative stock", func(t *testing.T) {
		err := testDB.Exec("UPDATE products SET stock = -1 WHERE id = ?", id).Error
		assert.Error(t, err, "CHECK (stock >= 0) is the last line of defence")
	})
}
