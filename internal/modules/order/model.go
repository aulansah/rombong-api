// Package order turns a shopping cart into an order and manages its
// lifecycle.
package order

import (
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of an order.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusPaid       Status = "PAID"
	StatusProcessing Status = "PROCESSING"
	StatusShipped    Status = "SHIPPED"
	StatusCompleted  Status = "COMPLETED"
	StatusCancelled  Status = "CANCELLED"
)

// adminTransitions is the state machine available through
// PATCH /orders/:id/status.
//
// PENDING -> PAID is deliberately absent: an order becomes PAID only through
// a successful payment. COMPLETED and CANCELLED are terminal.
var adminTransitions = map[Status][]Status{
	StatusPending:    {StatusCancelled},
	StatusPaid:       {StatusProcessing},
	StatusProcessing: {StatusShipped},
	StatusShipped:    {StatusCompleted},
}

// CanTransition reports whether an admin may move an order from one status
// to another.
func CanTransition(from, to Status) bool {
	for _, allowed := range adminTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Order is the orders table. Orders are never deleted.
type Order struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID      uuid.UUID
	OrderNumber string
	Status      Status
	TotalAmount int64
	Items       []Item `gorm:"foreignKey:OrderID"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Item is the order_items table. ProductName and UnitPrice are a snapshot
// taken at checkout: later changes to the product (or its deletion) must not
// rewrite history.
type Item struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	OrderID     uuid.UUID
	ProductID   *uuid.UUID
	ProductName string
	UnitPrice   int64
	Quantity    int
	Subtotal    int64
}

// TableName keeps the table called order_items (GORM would derive "items").
func (Item) TableName() string { return "order_items" }

// UpdateStatusRequest is the body of PATCH /orders/:id/status.
type UpdateStatusRequest struct {
	Status string `json:"status" validate:"required,oneof=PROCESSING SHIPPED COMPLETED CANCELLED"`
}

// ItemResponse is one line of an order.
type ItemResponse struct {
	ID          uuid.UUID  `json:"id"`
	ProductID   *uuid.UUID `json:"product_id"`
	ProductName string     `json:"product_name"`
	UnitPrice   int64      `json:"unit_price"`
	Quantity    int        `json:"quantity"`
	Subtotal    int64      `json:"subtotal"`
}

// Response is the public representation of an order.
type Response struct {
	ID          uuid.UUID      `json:"id"`
	UserID      uuid.UUID      `json:"user_id"`
	OrderNumber string         `json:"order_number"`
	Status      Status         `json:"status"`
	TotalAmount int64          `json:"total_amount"`
	Items       []ItemResponse `json:"items"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func ToResponse(o *Order) Response {
	res := Response{
		ID:          o.ID,
		UserID:      o.UserID,
		OrderNumber: o.OrderNumber,
		Status:      o.Status,
		TotalAmount: o.TotalAmount,
		Items:       make([]ItemResponse, 0, len(o.Items)),
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}
	for _, it := range o.Items {
		res.Items = append(res.Items, ItemResponse{
			ID:          it.ID,
			ProductID:   it.ProductID,
			ProductName: it.ProductName,
			UnitPrice:   it.UnitPrice,
			Quantity:    it.Quantity,
			Subtotal:    it.Subtotal,
		})
	}
	return res
}

func ToResponses(orders []Order) []Response {
	out := make([]Response, 0, len(orders))
	for i := range orders {
		out = append(out, ToResponse(&orders[i]))
	}
	return out
}
