package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aulansah/rombong-api/internal/modules/order"
	"github.com/aulansah/rombong-api/internal/utils"
	"github.com/aulansah/rombong-api/pkg/apperror"
)

// Repository is what the service needs from the persistence layer.
type Repository interface {
	Create(ctx context.Context, p *Payment) error
	FindByID(ctx context.Context, id uuid.UUID) (*Payment, error)
	FindByIdempotencyKey(ctx context.Context, key string) (*Payment, error)
	FindPendingByOrder(ctx context.Context, orderID uuid.UUID) (*Payment, error)
	FindByReference(ctx context.Context, ref string) (*Payment, error)
	SetCheckout(ctx context.Context, id uuid.UUID, token, redirectURL string) error
	Transition(ctx context.Context, id uuid.UUID, to Status, paidAt *time.Time) (bool, error)
}

// OrderStore is the part of the order repository payments need.
type OrderStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*order.Order, error)
	TransitionStatus(ctx context.Context, id uuid.UUID, from, to order.Status) (bool, error)
}

// Transactor runs a function inside one database transaction.
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Caller identifies who is making the request.
type Caller struct {
	UserID  uuid.UUID
	IsAdmin bool
}

type Service struct {
	repo    Repository
	orders  OrderStore
	tx      Transactor
	expiry  time.Duration
	gateway Gateway // nil: payments are simulated
}

// NewService builds the payment service. Pass a nil gateway to simulate
// payments (the simulate-success / simulate-failure endpoints).
func NewService(repo Repository, orders OrderStore, tx Transactor, expiry time.Duration, gateway Gateway) *Service {
	return &Service{repo: repo, orders: orders, tx: tx, expiry: expiry, gateway: gateway}
}

func (s *Service) provider() string {
	if s.gateway == nil {
		return ProviderSimulated
	}
	return s.gateway.Name()
}

// Create starts a payment for a PENDING order.
//
// The Idempotency-Key header makes the call safe to retry: sending the same
// key again returns the payment created the first time (created = false)
// instead of creating a second one. The unique index on idempotency_key is
// what guarantees this under concurrency; the lookup before the insert is
// only the fast path.
func (s *Service) Create(ctx context.Context, caller Caller, idempotencyKey string, req CreateRequest) (p *Payment, created bool, err error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return nil, false, apperror.BadRequest("Idempotency-Key header is required")
	}
	if len(key) > 100 {
		return nil, false, apperror.BadRequest("Idempotency-Key must be at most 100 characters")
	}
	orderID, err := uuid.Parse(req.OrderID)
	if err != nil {
		return nil, false, apperror.BadRequest("Invalid order_id")
	}

	o, err := s.ownedOrder(ctx, caller, orderID)
	if err != nil {
		return nil, false, err
	}

	if existing, err := s.replay(ctx, key, orderID); err != nil || existing != nil {
		return existing, false, err
	}

	if o.Status != order.StatusPending {
		return nil, false, apperror.Conflict(fmt.Sprintf("Order with status %s cannot be paid", o.Status))
	}
	if existing, err := s.activePayment(ctx, orderID, key); err != nil || existing != nil {
		return existing, false, err
	}

	ref, err := newReference()
	if err != nil {
		return nil, false, apperror.Internal(err)
	}
	p = &Payment{
		OrderID:        orderID,
		Reference:      ref,
		IdempotencyKey: key,
		Amount:         o.TotalAmount,
		Status:         StatusPending,
		Provider:       s.provider(),
		ExpiresAt:      time.Now().UTC().Add(s.expiry),
	}

	err = s.repo.Create(ctx, p)
	if errors.Is(err, apperror.ErrDuplicate) {
		// Lost a race. Either the same key was inserted concurrently (replay
		// it) or the order already has another active payment.
		if existing, rerr := s.replay(ctx, key, orderID); rerr != nil || existing != nil {
			return existing, false, rerr
		}
		return nil, false, apperror.Conflict("Order already has an active payment")
	}
	if err != nil {
		return nil, false, apperror.Internal(err)
	}
	if s.gateway != nil {
		if err := s.startCheckout(ctx, p); err != nil {
			return nil, false, err
		}
	}
	return p, true, nil
}

// startCheckout opens a checkout at the gateway. It runs after the payment
// row is committed, so a slow or failing gateway never holds a database
// transaction open. If the gateway fails, the payment is marked FAILED;
// the app then starts a new attempt with a new Idempotency-Key, exactly as
// after a declined payment.
func (s *Service) startCheckout(ctx context.Context, p *Payment) error {
	co, err := s.gateway.Charge(ctx, ChargeRequest{
		Reference:     p.Reference,
		Amount:        p.Amount,
		ExpiryMinutes: int(s.expiry.Minutes()),
	})
	if err != nil {
		if _, terr := s.repo.Transition(ctx, p.ID, StatusFailed, nil); terr != nil {
			return apperror.Internal(terr)
		}
		return apperror.New(http.StatusBadGateway, "GATEWAY_ERROR", "Payment gateway is unavailable, please try again")
	}
	if err := s.repo.SetCheckout(ctx, p.ID, co.Token, co.RedirectURL); err != nil {
		return apperror.Internal(err)
	}
	p.GatewayToken, p.RedirectURL = &co.Token, &co.RedirectURL
	return nil
}

// Get returns a payment if the caller owns its order (or is an admin).
func (s *Service) Get(ctx context.Context, caller Caller, id uuid.UUID) (*Payment, error) {
	p, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.NotFound("Payment not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if _, err := s.ownedOrder(ctx, caller, p.OrderID); err != nil {
		return nil, apperror.NotFound("Payment not found")
	}
	return p, nil
}

// SimulateSuccess marks the payment as paid and the order as PAID.
//
// Idempotency: calling it again on a payment that already succeeded returns
// the same result without doing anything. Under concurrency, the
// compare-and-swap in Transition lets exactly one request perform the
// change; both updates share a transaction, so a payment is never SUCCESS
// while its order is still PENDING.
func (s *Service) SimulateSuccess(ctx context.Context, caller Caller, id uuid.UUID) (*Payment, error) {
	if s.gateway != nil {
		return nil, errSimulationDisabled
	}
	p, err := s.Get(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	switch p.Status {
	case StatusSuccess:
		return p, nil
	case StatusFailed, StatusExpired:
		return nil, apperror.Conflict(fmt.Sprintf("Payment is already %s", p.Status))
	}
	if time.Now().After(p.ExpiresAt) {
		if _, err := s.repo.Transition(ctx, p.ID, StatusExpired, nil); err != nil {
			return nil, apperror.Internal(err)
		}
		return nil, apperror.Conflict("Payment has expired, please create a new payment")
	}

	alreadyProcessed, err := s.markPaid(ctx, p)
	if err != nil {
		return nil, err
	}

	current, err := s.Get(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if alreadyProcessed && current.Status != StatusSuccess {
		return nil, apperror.Conflict(fmt.Sprintf("Payment is already %s", current.Status))
	}
	return current, nil
}

// markPaid moves the payment to SUCCESS and its order to PAID in one
// transaction. alreadyProcessed is true when the payment had already left
// PENDING, in which case nothing changes.
func (s *Service) markPaid(ctx context.Context, p *Payment) (alreadyProcessed bool, err error) {
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		ok, err := s.repo.Transition(ctx, p.ID, StatusSuccess, &now)
		if err != nil {
			return err
		}
		if !ok {
			alreadyProcessed = true
			return nil
		}
		paid, err := s.orders.TransitionStatus(ctx, p.OrderID, order.StatusPending, order.StatusPaid)
		if err != nil {
			return err
		}
		if !paid {
			// The order was cancelled in the meantime. Returning an error
			// rolls the payment back to PENDING.
			return apperror.Conflict("Order is no longer awaiting payment")
		}
		return nil
	})
	if err != nil {
		return false, wrap(err)
	}
	return alreadyProcessed, nil
}

var errSimulationDisabled = apperror.Forbidden("Payment simulation is disabled when a payment gateway is configured")

// HandleNotification applies a gateway webhook. It is safe to receive the
// same notification many times: every change is a compare-and-swap from
// PENDING, so only the first one has an effect.
func (s *Service) HandleNotification(ctx context.Context, n Notification) error {
	if s.gateway == nil {
		return apperror.NotFound("Payment gateway is not configured")
	}
	if !s.gateway.VerifyNotification(n) {
		return apperror.Unauthorized("Invalid notification signature")
	}
	p, err := s.repo.FindByReference(ctx, n.OrderID)
	if errors.Is(err, apperror.ErrNotFound) {
		return apperror.NotFound("Payment not found")
	}
	if err != nil {
		return apperror.Internal(err)
	}
	if amount, ok := n.GrossAmountRupiah(); !ok || amount != p.Amount {
		return apperror.BadRequest("gross_amount does not match the payment amount")
	}

	switch n.Outcome() {
	case OutcomeSuccess:
		_, err = s.markPaid(ctx, p)
		return err
	case OutcomeFailed:
		_, err = s.repo.Transition(ctx, p.ID, StatusFailed, nil)
	case OutcomeExpired:
		_, err = s.repo.Transition(ctx, p.ID, StatusExpired, nil)
	}
	if err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// SimulateFailure marks the payment as FAILED. The order stays PENDING so
// the customer can try again with a new payment.
func (s *Service) SimulateFailure(ctx context.Context, caller Caller, id uuid.UUID) (*Payment, error) {
	if s.gateway != nil {
		return nil, errSimulationDisabled
	}
	p, err := s.Get(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	switch p.Status {
	case StatusFailed:
		return p, nil
	case StatusSuccess, StatusExpired:
		return nil, apperror.Conflict(fmt.Sprintf("Payment is already %s", p.Status))
	}

	ok, err := s.repo.Transition(ctx, p.ID, StatusFailed, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	current, err := s.Get(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if !ok && current.Status != StatusFailed {
		return nil, apperror.Conflict(fmt.Sprintf("Payment is already %s", current.Status))
	}
	return current, nil
}

// replay returns the payment previously created with this key, or nil.
// Reusing a key for a different order is a client bug and is rejected.
func (s *Service) replay(ctx context.Context, key string, orderID uuid.UUID) (*Payment, error) {
	existing, err := s.repo.FindByIdempotencyKey(ctx, key)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if existing.OrderID != orderID {
		return nil, apperror.Conflict("Idempotency-Key was already used for a different order")
	}
	return existing, nil
}

// activePayment looks at the order's current PENDING payment, if any:
//
//   - created with the same key: a concurrent request with this key won the
//     race after our first lookup, so it is returned as a replay;
//   - expired: it is marked EXPIRED, which frees the "one active payment per
//     order" slot, and nil is returned so a new payment can be created;
//   - otherwise the order already has an active payment (409).
func (s *Service) activePayment(ctx context.Context, orderID uuid.UUID, key string) (*Payment, error) {
	pending, err := s.repo.FindPendingByOrder(ctx, orderID)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if pending.IdempotencyKey == key {
		return pending, nil
	}
	if time.Now().Before(pending.ExpiresAt) {
		return nil, apperror.Conflict("Order already has an active payment")
	}
	if _, err := s.repo.Transition(ctx, pending.ID, StatusExpired, nil); err != nil {
		return nil, apperror.Internal(err)
	}
	return nil, nil
}

func (s *Service) ownedOrder(ctx context.Context, caller Caller, orderID uuid.UUID) (*order.Order, error) {
	o, err := s.orders.FindByID(ctx, orderID)
	if errors.Is(err, apperror.ErrNotFound) || (err == nil && !caller.IsAdmin && o.UserID != caller.UserID) {
		return nil, apperror.NotFound("Order not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return o, nil
}

func wrap(err error) error {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return apperror.Internal(err)
}

func newReference() (string, error) {
	suffix, err := utils.RandomHex(5)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("PAY-%s-%s", time.Now().UTC().Format("20060102"), strings.ToUpper(suffix)), nil
}
