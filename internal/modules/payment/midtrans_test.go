package payment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMidtransSignature(t *testing.T) {
	m := NewMidtrans("SB-Mid-server-test", false, "")
	n := Notification{OrderID: "PAY-20261121-ABCDEF0123", StatusCode: "200", GrossAmount: "273000.00"}
	n.SignatureKey = MidtransSignature(n, "SB-Mid-server-test")

	assert.Len(t, n.SignatureKey, 128, "SHA-512 hex")
	assert.True(t, m.VerifyNotification(n))

	tampered := n
	tampered.GrossAmount = "1000.00"
	assert.False(t, m.VerifyNotification(tampered), "amount changed after signing")

	other := NewMidtrans("another-key", false, "")
	assert.False(t, other.VerifyNotification(n), "signed with a different server key")
}

func TestNotificationOutcome(t *testing.T) {
	cases := map[string]struct {
		status, fraud string
		want          Outcome
	}{
		"settlement":        {"settlement", "", OutcomeSuccess},
		"capture accepted":  {"capture", "accept", OutcomeSuccess},
		"capture challenge": {"capture", "challenge", OutcomeNone},
		"pending":           {"pending", "", OutcomeNone},
		"deny":              {"deny", "", OutcomeFailed},
		"cancel":            {"cancel", "", OutcomeFailed},
		"expire":            {"expire", "", OutcomeExpired},
		"unknown":           {"refund", "", OutcomeNone},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, Notification{TransactionStatus: c.status, FraudStatus: c.fraud}.Outcome())
		})
	}
}

func TestGrossAmountRupiah(t *testing.T) {
	v, ok := Notification{GrossAmount: "273000.00"}.GrossAmountRupiah()
	assert.True(t, ok)
	assert.EqualValues(t, 273000, v)

	_, ok = Notification{GrossAmount: "10.50"}.GrossAmountRupiah()
	assert.False(t, ok, "rupiah has no cents")
	_, ok = Notification{GrossAmount: "abc"}.GrossAmountRupiah()
	assert.False(t, ok)
}

func TestMidtransCharge(t *testing.T) {
	var got snapRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/snap/v1/transactions", r.URL.Path)
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "SB-Mid-server-test", user)
		assert.Empty(t, pass)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"tok-1","redirect_url":"https://app.sandbox.midtrans.com/snap/v4/redirection/tok-1"}`))
	}))
	defer srv.Close()

	m := NewMidtrans("SB-Mid-server-test", false, srv.URL)
	co, err := m.Charge(t.Context(), ChargeRequest{Reference: "PAY-1", Amount: 85000, ExpiryMinutes: 30})
	require.NoError(t, err)
	assert.Equal(t, "tok-1", co.Token)
	assert.Contains(t, co.RedirectURL, "tok-1")
	assert.Equal(t, "PAY-1", got.TransactionDetails.OrderID)
	assert.EqualValues(t, 85000, got.TransactionDetails.GrossAmount)
	require.NotNil(t, got.Expiry)
	assert.Equal(t, 30, got.Expiry.Duration)
}

func TestMidtransChargeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error_messages":["Access denied due to unauthorized transaction"]}`))
	}))
	defer srv.Close()

	_, err := NewMidtrans("wrong", false, srv.URL).Charge(t.Context(), ChargeRequest{Reference: "PAY-1", Amount: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Access denied")
}

func TestMidtransBaseURL(t *testing.T) {
	assert.Equal(t, midtransSandboxURL, NewMidtrans("k", false, "").baseURL)
	assert.Equal(t, midtransProductionURL, NewMidtrans("k", true, "").baseURL)
}
