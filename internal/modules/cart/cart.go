// Package cart manages each customer's shopping cart.
package cart

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/aulansah/go-ecommerce-api/internal/database"
	"github.com/aulansah/go-ecommerce-api/internal/modules/product"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

// Cart is the carts table: exactly one cart per user.
type Cart struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    uuid.UUID
	Items     []Item `gorm:"foreignKey:CartID"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Item is the cart_items table. It stores no price: a cart always reflects
// the current product price, which is read through the Product relation.
type Item struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CartID    uuid.UUID
	ProductID uuid.UUID
	Product   product.Product `gorm:"foreignKey:ProductID"`
	Quantity  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName keeps the table called cart_items (GORM would derive "items").
func (Item) TableName() string { return "cart_items" }

// AddItemRequest is the body of POST /cart/items.
type AddItemRequest struct {
	ProductID string `json:"product_id" validate:"required,uuid"`
	Quantity  int    `json:"quantity" validate:"required,gt=0,lte=1000"`
}

// UpdateItemRequest is the body of PUT /cart/items/:id.
type UpdateItemRequest struct {
	Quantity int `json:"quantity" validate:"required,gt=0,lte=1000"`
}

// ItemResponse is one line of the cart.
type ItemResponse struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	ProductSlug string    `json:"product_slug"`
	UnitPrice   int64     `json:"unit_price"`
	Quantity    int       `json:"quantity"`
	Subtotal    int64     `json:"subtotal"`
	// Available is false when the product was removed or has less stock
	// than the requested quantity, so the client can warn before checkout.
	Available bool `json:"available"`
}

// Response is the cart with totals computed at read time.
type Response struct {
	ID          uuid.UUID      `json:"id"`
	Items       []ItemResponse `json:"items"`
	TotalItems  int            `json:"total_items"`
	TotalAmount int64          `json:"total_amount"`
}

// ToResponse computes subtotals and the cart total from current prices.
// Totals are derived on every read instead of being stored, so they can
// never drift from the items.
func ToResponse(c *Cart) Response {
	res := Response{ID: c.ID, Items: make([]ItemResponse, 0, len(c.Items))}
	for _, it := range c.Items {
		exists := it.Product.ID != uuid.Nil
		line := ItemResponse{
			ID:          it.ID,
			ProductID:   it.ProductID,
			ProductName: it.Product.Name,
			ProductSlug: it.Product.Slug,
			UnitPrice:   it.Product.Price,
			Quantity:    it.Quantity,
			Subtotal:    it.Product.Price * int64(it.Quantity),
			Available:   exists && it.Product.Stock >= it.Quantity,
		}
		res.Items = append(res.Items, line)
		res.TotalItems += it.Quantity
		res.TotalAmount += line.Subtotal
	}
	return res
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// GetOrCreate returns the user's cart with its items, creating an empty cart
// on first use. ON CONFLICT DO NOTHING makes the create safe when two
// requests arrive at the same time.
func (r *GormRepository) GetOrCreate(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	return r.load(ctx, userID, false)
}

// GetForCheckout is GetOrCreate plus a row lock (SELECT ... FOR UPDATE) on
// the cart. It must be called inside a transaction. The lock serializes
// concurrent checkouts of the same user: the second one waits, then finds the
// cart already emptied, so one cart can never become two orders.
func (r *GormRepository) GetForCheckout(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	return r.load(ctx, userID, true)
}

func (r *GormRepository) load(ctx context.Context, userID uuid.UUID, lock bool) (*Cart, error) {
	db := database.FromContext(ctx, r.db)

	err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).
		Create(&Cart{UserID: userID}).Error
	if err != nil {
		return nil, err
	}

	q := db
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var c Cart
	err = q.
		Preload("Items", func(tx *gorm.DB) *gorm.DB { return tx.Order("cart_items.created_at ASC") }).
		Preload("Items.Product").
		First(&c, "user_id = ?", userID).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *GormRepository) FindItem(ctx context.Context, cartID, itemID uuid.UUID) (*Item, error) {
	var it Item
	err := database.FromContext(ctx, r.db).First(&it, "id = ? AND cart_id = ?", itemID, cartID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &it, err
}

// UpsertItem sets the quantity of a product in the cart, inserting the row
// if the product is not in the cart yet.
func (r *GormRepository) UpsertItem(ctx context.Context, cartID, productID uuid.UUID, quantity int) error {
	return database.FromContext(ctx, r.db).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cart_id"}, {Name: "product_id"}},
		DoUpdates: clause.Assignments(map[string]any{"quantity": quantity, "updated_at": time.Now().UTC()}),
	}).Create(&Item{CartID: cartID, ProductID: productID, Quantity: quantity}).Error
}

func (r *GormRepository) UpdateItemQuantity(ctx context.Context, itemID uuid.UUID, quantity int) error {
	return database.FromContext(ctx, r.db).Model(&Item{}).
		Where("id = ?", itemID).Update("quantity", quantity).Error
}

func (r *GormRepository) DeleteItem(ctx context.Context, itemID uuid.UUID) error {
	return database.FromContext(ctx, r.db).Delete(&Item{}, "id = ?", itemID).Error
}

// Clear removes every item from the cart.
func (r *GormRepository) Clear(ctx context.Context, cartID uuid.UUID) error {
	return database.FromContext(ctx, r.db).Delete(&Item{}, "cart_id = ?", cartID).Error
}
