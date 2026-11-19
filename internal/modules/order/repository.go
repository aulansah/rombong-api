package order

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/aulansah/rombong-api/internal/database"
	"github.com/aulansah/rombong-api/pkg/apperror"
)

type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// Create inserts the order together with its items.
func (r *GormRepository) Create(ctx context.Context, o *Order) error {
	return database.FromContext(ctx, r.db).Create(o).Error
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	var o Order
	err := database.FromContext(ctx, r.db).Preload("Items").First(&o, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &o, err
}

// List returns orders newest first. A nil userID lists every user's orders
// (admin view).
func (r *GormRepository) List(ctx context.Context, userID *uuid.UUID, offset, limit int) ([]Order, int64, error) {
	q := database.FromContext(ctx, r.db).Model(&Order{})
	if userID != nil {
		q = q.Where("user_id = ?", *userID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var orders []Order
	err := q.Preload("Items").Order("created_at DESC").Offset(offset).Limit(limit).Find(&orders).Error
	return orders, total, err
}

// TransitionStatus is a compare-and-swap on the status column:
//
//	UPDATE orders SET status = $to WHERE id = $id AND status = $from
//
// It returns false when the order was not in the expected status any more,
// which is how concurrent cancel/pay/ship requests are kept consistent
// without explicit locks.
func (r *GormRepository) TransitionStatus(ctx context.Context, id uuid.UUID, from, to Status) (bool, error) {
	res := database.FromContext(ctx, r.db).Model(&Order{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	return res.RowsAffected == 1, res.Error
}
