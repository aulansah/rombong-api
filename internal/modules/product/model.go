// Package product manages the product catalog, stock and its Redis cache.
package product

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrInsufficientStock is returned when a stock decrement would go below zero
// (or the product no longer exists).
var ErrInsufficientStock = errors.New("insufficient stock")

// Product is the products table. Price is an integer amount in the smallest
// currency unit (rupiah), never a float.
type Product struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CategoryID  uuid.UUID
	Name        string
	Slug        string
	Description string
	Price       int64
	Stock       int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

// SaveRequest is the body of POST /products and PUT /products/:id.
// Stock is a pointer so that an explicit 0 is distinguishable from "missing".
type SaveRequest struct {
	Name        string `json:"name" validate:"required,min=3,max=200"`
	Description string `json:"description" validate:"max=5000"`
	Price       int64  `json:"price" validate:"required,gt=0"`
	Stock       *int   `json:"stock" validate:"required,gte=0"`
	CategoryID  string `json:"category_id" validate:"required,uuid"`
}

// StockRequest is the body of PATCH /products/:id/stock.
type StockRequest struct {
	Stock *int `json:"stock" validate:"required,gte=0"`
}

// ListFilter holds the normalized query parameters of GET /products.
type ListFilter struct {
	Search     string     `json:"search"`
	CategoryID *uuid.UUID `json:"category_id"`
	Sort       string     `json:"sort"`
	Order      string     `json:"order"`
	Page       int        `json:"page"`
	Limit      int        `json:"limit"`
}

// Response is the public representation of a product. It is also the shape
// stored in Redis.
type Response struct {
	ID          uuid.UUID `json:"id"`
	CategoryID  uuid.UUID `json:"category_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Price       int64     `json:"price"`
	Stock       int       `json:"stock"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func ToResponse(p *Product) Response {
	return Response{
		ID:          p.ID,
		CategoryID:  p.CategoryID,
		Name:        p.Name,
		Slug:        p.Slug,
		Description: p.Description,
		Price:       p.Price,
		Stock:       p.Stock,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// ListResult is one page of products plus the total match count.
type ListResult struct {
	Items []Response `json:"items"`
	Total int64      `json:"total"`
}
