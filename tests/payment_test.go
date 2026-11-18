//go:build integration

package tests

import (
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func orderStatus(t testing.TB, orderID string) string {
	t.Helper()
	var status string
	require.NoError(t, testDB.Raw("SELECT status FROM orders WHERE id = ?", orderID).Scan(&status).Error)
	return status
}

func paymentCount(t testing.TB, orderID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM payments WHERE order_id = ?", orderID).Scan(&n).Error)
	return n
}

func TestPaymentFlow(t *testing.T) {
	resetDB(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Minuman")
	productID := createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 50)

	t.Run("create requires an idempotency key and an own, payable order", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)

		res := call(t, http.MethodPost, "/payments", customer, map[string]string{"order_id": orderID})
		assert.Equal(t, http.StatusBadRequest, res.Status, "missing Idempotency-Key")

		res = createPayment(t, customer, uuid.NewString(), uuid.NewString())
		assert.Equal(t, http.StatusNotFound, res.Status, "unknown order")

		res = createPayment(t, newCustomer(t), orderID, uuid.NewString())
		assert.Equal(t, http.StatusNotFound, res.Status, "someone else's order")

		res = call(t, http.MethodPost, "/payments", customer, map[string]any{"order_id": orderID, "amount": 1}, "Idempotency-Key", uuid.NewString())
		require.Equal(t, http.StatusCreated, res.Status, res.Raw)
		assert.EqualValues(t, 85000, res.Data()["amount"], "amount comes from the order, not the client")
		assert.Equal(t, "PENDING", res.Data()["status"])
		assert.Regexp(t, `^PAY-\d{8}-[0-9A-F]{10}$`, res.Data()["reference"])
	})

	t.Run("same idempotency key returns the same payment", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		key := uuid.NewString()

		first := createPayment(t, customer, orderID, key)
		require.Equal(t, http.StatusCreated, first.Status, first.Raw)
		second := createPayment(t, customer, orderID, key)
		require.Equal(t, http.StatusOK, second.Status, second.Raw)

		assert.Equal(t, first.Data()["id"], second.Data()["id"])
		assert.Equal(t, first.Data()["reference"], second.Data()["reference"])
		assert.EqualValues(t, 1, paymentCount(t, orderID))

		t.Run("a different key cannot open a second active payment", func(t *testing.T) {
			res := createPayment(t, customer, orderID, uuid.NewString())
			assert.Equal(t, http.StatusConflict, res.Status)
			assert.EqualValues(t, 1, paymentCount(t, orderID))
		})

		t.Run("a key cannot be reused for another order", func(t *testing.T) {
			otherOrder := placeOrder(t, customer, productID, 1)["id"].(string)
			res := createPayment(t, customer, otherOrder, key)
			assert.Equal(t, http.StatusConflict, res.Status)
		})
	})

	t.Run("concurrent creates with one key produce one payment", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		key := uuid.NewString()

		ids := make(chan string, 10)
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := createPayment(t, customer, orderID, key)
				if res.Status == http.StatusCreated || res.Status == http.StatusOK {
					ids <- res.Data()["id"].(string)
				} else {
					ids <- "unexpected status: " + res.Raw
				}
			}()
		}
		wg.Wait()
		close(ids)

		unique := map[string]bool{}
		for id := range ids {
			unique[id] = true
		}
		assert.Len(t, unique, 1, "every request sees the same payment: %v", unique)
		assert.EqualValues(t, 1, paymentCount(t, orderID))
	})

	t.Run("simulate-success is idempotent and marks the order PAID", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		paymentID := createPayment(t, customer, orderID, uuid.NewString()).Data()["id"].(string)

		assert.Equal(t, http.StatusNotFound,
			call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", newCustomer(t), nil).Status,
			"another user cannot pay or even see the payment")

		first := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", customer, nil)
		require.Equal(t, http.StatusOK, first.Status, first.Raw)
		assert.Equal(t, "SUCCESS", first.Data()["status"])
		assert.NotNil(t, first.Data()["paid_at"])
		assert.Equal(t, "PAID", orderStatus(t, orderID))

		second := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", customer, nil)
		require.Equal(t, http.StatusOK, second.Status, second.Raw)
		assert.Equal(t, first.Data()["paid_at"], second.Data()["paid_at"], "the repeat changed nothing")
		assert.Equal(t, first.Data()["updated_at"], second.Data()["updated_at"])

		res := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-failure", customer, nil)
		assert.Equal(t, http.StatusConflict, res.Status, "a successful payment cannot fail afterwards")
		assert.Equal(t, "PAID", orderStatus(t, orderID))

		res = createPayment(t, customer, orderID, uuid.NewString())
		assert.Equal(t, http.StatusConflict, res.Status, "a paid order cannot be paid again")

		res = call(t, http.MethodGet, "/payments/"+paymentID, customer, nil)
		require.Equal(t, http.StatusOK, res.Status)
		assert.Equal(t, "SUCCESS", res.Data()["status"])
	})

	t.Run("concurrent simulate-success processes the payment once", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		paymentID := createPayment(t, customer, orderID, uuid.NewString()).Data()["id"].(string)

		paidAt := make(chan string, 10)
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", customer, nil)
				if res.Status != http.StatusOK {
					paidAt <- "unexpected status: " + res.Raw
					return
				}
				paidAt <- res.Data()["paid_at"].(string)
			}()
		}
		wg.Wait()
		close(paidAt)

		unique := map[string]bool{}
		for v := range paidAt {
			unique[v] = true
		}
		assert.Len(t, unique, 1, "all callers observe the single paid_at: %v", unique)
		assert.Equal(t, "PAID", orderStatus(t, orderID))
	})

	t.Run("failed payment keeps the order payable", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		paymentID := createPayment(t, customer, orderID, uuid.NewString()).Data()["id"].(string)

		res := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-failure", customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Equal(t, "FAILED", res.Data()["status"])
		assert.Equal(t, "PENDING", orderStatus(t, orderID))

		res = call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-failure", customer, nil)
		assert.Equal(t, http.StatusOK, res.Status, "repeat is idempotent")

		res = call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", customer, nil)
		assert.Equal(t, http.StatusConflict, res.Status, "a failed payment cannot succeed afterwards")

		retry := createPayment(t, customer, orderID, uuid.NewString())
		require.Equal(t, http.StatusCreated, retry.Status, retry.Raw)
		res = call(t, http.MethodPost, "/payments/"+retry.Data()["id"].(string)+"/simulate-success", customer, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Equal(t, "PAID", orderStatus(t, orderID))
		assert.EqualValues(t, 2, paymentCount(t, orderID))
	})

	t.Run("expired payment cannot be paid and frees the order for a new one", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		paymentID := createPayment(t, customer, orderID, uuid.NewString()).Data()["id"].(string)
		require.NoError(t, testDB.Exec("UPDATE payments SET expires_at = now() - interval '1 minute' WHERE id = ?", paymentID).Error)

		res := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", customer, nil)
		assert.Equal(t, http.StatusConflict, res.Status)
		assert.Equal(t, "PENDING", orderStatus(t, orderID))
		assert.Equal(t, "EXPIRED", call(t, http.MethodGet, "/payments/"+paymentID, customer, nil).Data()["status"])

		res = createPayment(t, customer, orderID, uuid.NewString())
		assert.Equal(t, http.StatusCreated, res.Status, res.Raw)
	})

	t.Run("a cancelled order cannot be paid", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		paymentID := createPayment(t, customer, orderID, uuid.NewString()).Data()["id"].(string)
		require.Equal(t, http.StatusOK, call(t, http.MethodPost, "/orders/"+orderID+"/cancel", customer, nil).Status)

		res := call(t, http.MethodPost, "/payments/"+paymentID+"/simulate-success", customer, nil)
		assert.Equal(t, http.StatusConflict, res.Status)
		assert.Equal(t, "CANCELLED", orderStatus(t, orderID))
		assert.Equal(t, "PENDING", call(t, http.MethodGet, "/payments/"+paymentID, customer, nil).Data()["status"],
			"the payment update was rolled back together with the failed order update")

		res = createPayment(t, customer, orderID, uuid.NewString())
		assert.Equal(t, http.StatusConflict, res.Status)
	})
}
