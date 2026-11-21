package payment

import (
	"bytes"
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Gateway starts a checkout at an external payment provider. A nil Gateway
// means payments are simulated.
type Gateway interface {
	Name() string
	Charge(ctx context.Context, req ChargeRequest) (Checkout, error)
	// VerifyNotification checks that a webhook really came from the provider.
	VerifyNotification(n Notification) bool
}

// ChargeRequest is what the provider needs to open a checkout page.
type ChargeRequest struct {
	Reference     string // our payment reference, used as the provider's order_id
	Amount        int64  // rupiah
	ExpiryMinutes int
}

// Checkout is the provider's answer: a token and a page to send the buyer to.
type Checkout struct {
	Token       string
	RedirectURL string
}

// Notification is the body of a Midtrans HTTP notification (webhook).
// Only the fields Rombong uses are listed.
type Notification struct {
	OrderID           string `json:"order_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	TransactionID     string `json:"transaction_id"`
	PaymentType       string `json:"payment_type"`
}

// Outcome is what a notification means for a payment.
type Outcome int

const (
	OutcomeNone Outcome = iota // still pending, nothing to do
	OutcomeSuccess
	OutcomeFailed
	OutcomeExpired
)

// Outcome maps Midtrans transaction_status (and fraud_status for card
// captures) to a payment outcome.
func (n Notification) Outcome() Outcome {
	switch n.TransactionStatus {
	case "settlement":
		return OutcomeSuccess
	case "capture":
		if n.FraudStatus == "" || n.FraudStatus == "accept" {
			return OutcomeSuccess
		}
		return OutcomeNone // "challenge": wait for a later notification
	case "deny", "cancel", "failure":
		return OutcomeFailed
	case "expire":
		return OutcomeExpired
	default: // "pending", "authorize", refunds and anything new
		return OutcomeNone
	}
}

// GrossAmountRupiah parses "273000.00" into 273000.
func (n Notification) GrossAmountRupiah() (int64, bool) {
	f, err := strconv.ParseFloat(n.GrossAmount, 64)
	if err != nil || f < 0 {
		return 0, false
	}
	return int64(f), float64(int64(f)) == f
}

const (
	midtransSandboxURL    = "https://app.sandbox.midtrans.com"
	midtransProductionURL = "https://app.midtrans.com"
)

// Midtrans is a Gateway backed by the Midtrans Snap API.
type Midtrans struct {
	serverKey string
	baseURL   string
	http      *http.Client
}

// NewMidtrans builds a Snap client. baseURL may be empty, in which case the
// sandbox or production URL is chosen from production.
func NewMidtrans(serverKey string, production bool, baseURL string) *Midtrans {
	if baseURL == "" {
		baseURL = midtransSandboxURL
		if production {
			baseURL = midtransProductionURL
		}
	}
	return &Midtrans{
		serverKey: serverKey,
		baseURL:   strings.TrimRight(baseURL, "/"),
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *Midtrans) Name() string { return ProviderMidtrans }

type snapRequest struct {
	TransactionDetails struct {
		OrderID     string `json:"order_id"`
		GrossAmount int64  `json:"gross_amount"`
	} `json:"transaction_details"`
	Expiry *snapExpiry `json:"expiry,omitempty"`
}

type snapExpiry struct {
	Unit     string `json:"unit"`
	Duration int    `json:"duration"`
}

type snapResponse struct {
	Token         string   `json:"token"`
	RedirectURL   string   `json:"redirect_url"`
	ErrorMessages []string `json:"error_messages"`
}

// Charge creates a Snap transaction. Midtrans answers 201 with a token and
// a redirect_url to its hosted payment page.
func (m *Midtrans) Charge(ctx context.Context, req ChargeRequest) (Checkout, error) {
	var body snapRequest
	body.TransactionDetails.OrderID = req.Reference
	body.TransactionDetails.GrossAmount = req.Amount
	if req.ExpiryMinutes > 0 {
		body.Expiry = &snapExpiry{Unit: "minute", Duration: req.ExpiryMinutes}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Checkout{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/snap/v1/transactions", bytes.NewReader(raw))
	if err != nil {
		return Checkout{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(m.serverKey, "") // Midtrans: server key as user, empty password

	res, err := m.http.Do(httpReq)
	if err != nil {
		return Checkout{}, fmt.Errorf("midtrans: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Checkout{}, fmt.Errorf("midtrans: read response: %w", err)
	}

	var out snapResponse
	_ = json.Unmarshal(data, &out)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		return Checkout{}, fmt.Errorf("midtrans: status %d: %s", res.StatusCode, strings.Join(out.ErrorMessages, "; "))
	}
	if out.Token == "" || out.RedirectURL == "" {
		return Checkout{}, fmt.Errorf("midtrans: response without token or redirect_url")
	}
	return Checkout{Token: out.Token, RedirectURL: out.RedirectURL}, nil
}

// VerifyNotification checks signature_key, which Midtrans computes as
// SHA512(order_id + status_code + gross_amount + server_key).
func (m *Midtrans) VerifyNotification(n Notification) bool {
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(n.SignatureKey)), []byte(MidtransSignature(n, m.serverKey))) == 1
}

// MidtransSignature returns the expected signature_key for a notification.
func MidtransSignature(n Notification, serverKey string) string {
	sum := sha512.Sum512([]byte(n.OrderID + n.StatusCode + n.GrossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}
