package product

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/aulansah/go-ecommerce-api/internal/database"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) Create(ctx context.Context, p *Product) error {
	err := database.FromContext(ctx, r.db).Create(p).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.ErrDuplicate
	}
	return err
}

// Update writes the editable columns. Select is explicit so zero values
// (stock 0, empty description) are persisted instead of being skipped.
func (r *GormRepository) Update(ctx context.Context, p *Product) error {
	return database.FromContext(ctx, r.db).Model(p).
		Select("category_id", "name", "description", "price", "stock").
		Updates(p).Error
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return database.FromContext(ctx, r.db).Delete(&Product{}, "id = ?", id).Error
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (*Product, error) {
	var p Product
	err := database.FromContext(ctx, r.db).First(&p, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &p, err
}

// List applies search, category filter, sorting and pagination.
// f.Sort and f.Order must already be whitelisted by the service: column names
// cannot be bound as query parameters, so they are the one place where
// unvalidated input would allow SQL injection.
func (r *GormRepository) List(ctx context.Context, f ListFilter) ([]Product, int64, error) {
	q := database.FromContext(ctx, r.db).Model(&Product{})

	if f.Search != "" {
		q = q.Where("name ILIKE ?", "%"+escapeLike(f.Search)+"%")
	}
	if f.CategoryID != nil {
		q = q.Where("category_id = ?", *f.CategoryID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var products []Product
	err := q.
		Order(clause.OrderByColumn{Column: clause.Column{Name: f.Sort}, Desc: f.Order == "desc"}).
		Order("id ASC"). // tie-breaker keeps pagination stable
		Offset((f.Page - 1) * f.Limit).
		Limit(f.Limit).
		Find(&products).Error
	return products, total, err
}

// SetStock overwrites the stock with an absolute value (admin restock).
func (r *GormRepository) SetStock(ctx context.Context, id uuid.UUID, stock int) error {
	return database.FromContext(ctx, r.db).Model(&Product{}).
		Where("id = ?", id).Update("stock", stock).Error
}

// DecreaseStock atomically reserves qty units and returns the updated row.
//
// The whole check-and-decrement is a single statement:
//
//	UPDATE products SET stock = stock - $qty
//	WHERE id = $id AND stock >= $qty AND deleted_at IS NULL
//	RETURNING *
//
// PostgreSQL takes a row lock for the UPDATE, so two concurrent checkouts
// cannot both pass the "stock >= qty" condition for the last unit. When no
// row is affected the product is out of stock (or gone).
func (r *GormRepository) DecreaseStock(ctx context.Context, id uuid.UUID, qty int) (*Product, error) {
	var p Product
	res := database.FromContext(ctx, r.db).Model(&p).
		Clauses(clause.Returning{}).
		Where("id = ? AND stock >= ?", id, qty).
		Updates(map[string]any{
			"stock":      gorm.Expr("stock - ?", qty),
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrInsufficientStock
	}
	return &p, nil
}

// IncreaseStock returns units to stock (order cancellation). Unscoped is used
// so stock is restored even if the product was soft-deleted in the meantime.
func (r *GormRepository) IncreaseStock(ctx context.Context, id uuid.UUID, qty int) error {
	return database.FromContext(ctx, r.db).Unscoped().Model(&Product{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"stock":      gorm.Expr("stock + ?", qty),
			"updated_at": time.Now().UTC(),
		}).Error
}

// escapeLike escapes the LIKE wildcards so user input is matched literally.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
