//go:build integration

package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/aulansah/rombong-api/internal/app"
	"github.com/aulansah/rombong-api/internal/cache"
	"github.com/aulansah/rombong-api/internal/config"
	"github.com/aulansah/rombong-api/internal/modules/payment"
)

const midtransKey = "SB-Mid-server-integration-test"

// fakeSnap stands in for the Midtrans Snap API.
type fakeSnap struct {
	mu       sync.Mutex
	orderIDs []string
	amounts  []int64
	down     atomic.Bool
}

func (f *fakeSnap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if user, _, ok := r.BasicAuth(); !ok || user != midtransKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.down.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error_messages":["service unavailable"]}`))
		return
	}
	var body struct {
		TransactionDetails struct {
			OrderID     string `json:"order_id"`
			GrossAmount int64  `json:"gross_amount"`
		} `json:"transaction_details"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.orderIDs = append(f.orderIDs, body.TransactionDetails.OrderID)
	f.amounts = append(f.amounts, body.TransactionDetails.GrossAmount)
	f.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token":        "tok-" + body.TransactionDetails.OrderID,
		"redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/tok-" + body.TransactionDetails.OrderID,
	})
}

// withMidtransApp swaps the shared test app for one configured with
// PAYMENT_PROVIDER=midtrans pointing at a fake Snap server.
func withMidtransApp(t *testing.T) *fakeSnap {
	t.Helper()
	snap := &fakeSnap{}
	srv := httptest.NewServer(snap)
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	cfg := &config.Config{
		App:       config.AppConfig{Name: "rombong-api-test", Env: "test", BcryptCost: bcrypt.MinCost},
		JWT:       config.JWTConfig{Secret: "integration-test-secret-with-32-chars!!", AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour},
		CORS:      config.CORSConfig{AllowOrigins: "http://localhost:3000"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Cache:     config.CacheConfig{ProductTTL: time.Minute, ProductListTTL: time.Minute},
		Payment: config.PaymentConfig{
			Expiry:   30 * time.Minute,
			Provider: payment.ProviderMidtrans,
			Midtrans: config.MidtransConfig{ServerKey: midtransKey, BaseURL: srv.URL},
		},
	}
	previous := testApp
	testApp = app.New(app.Deps{Config: cfg, DB: testDB, Cache: cache.NewRedisFromClient(rc), Log: zap.NewNop()})
	t.Cleanup(func() {
		testApp = previous
		_ = rc.Close()
		srv.Close()
	})
	return snap
}

func notify(t *testing.T, reference, status, amount, key string) result {
	t.Helper()
	n := payment.Notification{OrderID: reference, StatusCode: "200", GrossAmount: amount, TransactionStatus: status, TransactionID: uuid.NewString()}
	n.SignatureKey = payment.MidtransSignature(n, key)
	return call(t, http.MethodPost, "/webhooks/midtrans", "", n)
}

func paymentStatus(t *testing.T, id string) string {
	t.Helper()
	var s string
	require.NoError(t, testDB.Raw("SELECT status FROM payments WHERE id = ?", id).Scan(&s).Error)
	return s
}

func TestMidtransPayment(t *testing.T) {
	resetDB(t)
	snap := withMidtransApp(t)
	admin := adminToken(t)
	customer := newCustomer(t)
	categoryID := createCategory(t, admin, "Minuman")
	productID := createProduct(t, admin, categoryID, "Kopi Arabika", 85000, 50)

	t.Run("create opens a Snap checkout and returns its redirect_url", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 2)["id"].(string)
		key := uuid.NewString()
		res := createPayment(t, customer, orderID, key)
		require.Equal(t, http.StatusCreated, res.Status, res.Raw)
		ref := res.Data()["reference"].(string)
		assert.Equal(t, "midtrans", res.Data()["provider"])
		assert.Contains(t, res.Data()["redirect_url"], "tok-"+ref)
		assert.Contains(t, snap.orderIDs, ref, "Midtrans order_id is our payment reference")
		assert.Contains(t, snap.amounts, int64(170000), "amount comes from the order")

		replay := createPayment(t, customer, orderID, key)
		require.Equal(t, http.StatusOK, replay.Status, replay.Raw)
		assert.Equal(t, res.Data()["redirect_url"], replay.Data()["redirect_url"], "replay returns the same checkout")
		assert.Len(t, snap.orderIDs, 1, "a replay does not open a second checkout")
	})

	t.Run("simulation endpoints are disabled", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		p := createPayment(t, customer, orderID, uuid.NewString())
		require.Equal(t, http.StatusCreated, p.Status, p.Raw)
		res := call(t, http.MethodPost, "/payments/"+p.Data()["id"].(string)+"/simulate-success", customer, nil)
		assert.Equal(t, http.StatusForbidden, res.Status)
		assert.Equal(t, "PENDING", orderStatus(t, orderID))
	})

	t.Run("settlement webhook pays the order once", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		p := createPayment(t, customer, orderID, uuid.NewString())
		require.Equal(t, http.StatusCreated, p.Status, p.Raw)
		ref, id := p.Data()["reference"].(string), p.Data()["id"].(string)

		bad := notify(t, ref, "settlement", "85000.00", "not-the-server-key")
		assert.Equal(t, http.StatusUnauthorized, bad.Status, "forged signature")
		assert.Equal(t, "PENDING", orderStatus(t, orderID))

		wrongAmount := notify(t, ref, "settlement", "1000.00", midtransKey)
		assert.Equal(t, http.StatusBadRequest, wrongAmount.Status)

		pending := notify(t, ref, "pending", "85000.00", midtransKey)
		assert.Equal(t, http.StatusOK, pending.Status)
		assert.Equal(t, "PENDING", paymentStatus(t, id))

		var wg sync.WaitGroup
		codes := make([]int, 10)
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i] = notify(t, ref, "settlement", "85000.00", midtransKey).Status
			}(i)
		}
		wg.Wait()
		for _, c := range codes {
			assert.Equal(t, http.StatusOK, c, "Midtrans retries must all be acknowledged")
		}
		assert.Equal(t, "SUCCESS", paymentStatus(t, id))
		assert.Equal(t, "PAID", orderStatus(t, orderID))

		late := notify(t, ref, "expire", "85000.00", midtransKey)
		assert.Equal(t, http.StatusOK, late.Status)
		assert.Equal(t, "SUCCESS", paymentStatus(t, id), "a final status is never overwritten")
	})

	t.Run("expire webhook frees the order for a new attempt", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		p := createPayment(t, customer, orderID, uuid.NewString())
		require.Equal(t, http.StatusCreated, p.Status, p.Raw)
		res := notify(t, p.Data()["reference"].(string), "expire", "85000.00", midtransKey)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		assert.Equal(t, "EXPIRED", paymentStatus(t, p.Data()["id"].(string)))

		again := createPayment(t, customer, orderID, uuid.NewString())
		assert.Equal(t, http.StatusCreated, again.Status, again.Raw)
	})

	t.Run("gateway outage fails the attempt cleanly", func(t *testing.T) {
		orderID := placeOrder(t, customer, productID, 1)["id"].(string)
		snap.down.Store(true)
		res := createPayment(t, customer, orderID, uuid.NewString())
		snap.down.Store(false)
		assert.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
		assert.Equal(t, "GATEWAY_ERROR", res.ErrorCode())
		assert.EqualValues(t, 1, paymentCount(t, orderID))

		retry := createPayment(t, customer, orderID, uuid.NewString())
		require.Equal(t, http.StatusCreated, retry.Status, retry.Raw)
		assert.NotEmpty(t, retry.Data()["redirect_url"])
	})

	t.Run("unknown payment reference", func(t *testing.T) {
		res := notify(t, "PAY-00000000-0000000000", "settlement", "1.00", midtransKey)
		assert.Equal(t, http.StatusNotFound, res.Status)
	})
}

func TestWebhookWithoutGateway(t *testing.T) {
	resetDB(t)
	res := notify(t, "PAY-00000000-0000000000", "settlement", "1.00", midtransKey)
	assert.Equal(t, http.StatusNotFound, res.Status, "simulated mode has no webhook")
}
