// Package payment takes payment for orders, either simulated or through
// the Midtrans Snap gateway.
package payment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/aulansah/rombong-api/internal/database"
	"github.com/aulansah/rombong-api/pkg/apperror"
)

// Status is the state of a payment.
type Status string

const (
	StatusPending Status = "PENDING"
	StatusSuccess Status = "SUCCESS"
	StatusFailed  Status = "FAILED"
	StatusExpired Status = "EXPIRED"
)

// Providers.
const (
	ProviderSimulated = "simulated"
	ProviderMidtrans  = "midtrans"
)

// Payment is the payments table.
type Payment struct {
	ID             uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrderID        uuid.UUID
	Reference      string
	IdempotencyKey string
	Amount         int64
	Status         Status
	Provider       string
	GatewayToken   *string
	RedirectURL    *string
	PaidAt         *time.Time
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateRequest is the body of POST /payments. There is no amount field:
// the amount is always the order total stored on the server.
type CreateRequest struct {
	OrderID string `json:"order_id" validate:"required,uuid"`
}

// Response is the public representation of a payment.
type Response struct {
	ID        uuid.UUID `json:"id"`
	OrderID   uuid.UUID `json:"order_id"`
	Reference string    `json:"reference"`
	Amount    int64     `json:"amount"`
	Status    Status    `json:"status"`
	// Provider is "simulated" or "midtrans". For midtrans, the app opens
	// RedirectURL so the buyer can pay on the Midtrans page.
	Provider    string     `json:"provider"`
	RedirectURL *string    `json:"redirect_url,omitempty"`
	PaidAt      *time.Time `json:"paid_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func ToResponse(p *Payment) Response {
	return Response{
		ID:          p.ID,
		OrderID:     p.OrderID,
		Reference:   p.Reference,
		Amount:      p.Amount,
		Status:      p.Status,
		Provider:    p.Provider,
		RedirectURL: p.RedirectURL,
		PaidAt:      p.PaidAt,
		ExpiresAt:   p.ExpiresAt,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) Create(ctx context.Context, p *Payment) error {
	err := database.FromContext(ctx, r.db).Create(p).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.ErrDuplicate
	}
	return err
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (*Payment, error) {
	var p Payment
	err := database.FromContext(ctx, r.db).First(&p, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &p, err
}

func (r *GormRepository) FindByIdempotencyKey(ctx context.Context, key string) (*Payment, error) {
	var p Payment
	err := database.FromContext(ctx, r.db).First(&p, "idempotency_key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &p, err
}

func (r *GormRepository) FindByReference(ctx context.Context, ref string) (*Payment, error) {
	var p Payment
	err := database.FromContext(ctx, r.db).First(&p, "reference = ?", ref).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &p, err
}

// SetCheckout stores the gateway token and payment page URL.
func (r *GormRepository) SetCheckout(ctx context.Context, id uuid.UUID, token, redirectURL string) error {
	return database.FromContext(ctx, r.db).Model(&Payment{}).Where("id = ?", id).
		Updates(map[string]any{"gateway_token": token, "redirect_url": redirectURL}).Error
}

func (r *GormRepository) FindPendingByOrder(ctx context.Context, orderID uuid.UUID) (*Payment, error) {
	var p Payment
	err := database.FromContext(ctx, r.db).First(&p, "order_id = ? AND status = ?", orderID, StatusPending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &p, err
}

// Transition is a compare-and-swap from PENDING to a final status. It
// returns false when the payment had already left PENDING, which is what
// makes processing a payment twice impossible.
func (r *GormRepository) Transition(ctx context.Context, id uuid.UUID, to Status, paidAt *time.Time) (bool, error) {
	res := database.FromContext(ctx, r.db).Model(&Payment{}).
		Where("id = ? AND status = ?", id, StatusPending).
		Updates(map[string]any{"status": to, "paid_at": paidAt})
	return res.RowsAffected == 1, res.Error
}
