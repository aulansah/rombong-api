package order

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aulansah/rombong-api/internal/modules/cart"
	"github.com/aulansah/rombong-api/internal/modules/product"
	"github.com/aulansah/rombong-api/internal/utils"
	"github.com/aulansah/rombong-api/pkg/apperror"
)

// Repository is what the service needs from the persistence layer.
type Repository interface {
	Create(ctx context.Context, o *Order) error
	FindByID(ctx context.Context, id uuid.UUID) (*Order, error)
	List(ctx context.Context, userID *uuid.UUID, offset, limit int) ([]Order, int64, error)
	TransitionStatus(ctx context.Context, id uuid.UUID, from, to Status) (bool, error)
}

// CartStore gives the order service access to the user's cart.
type CartStore interface {
	GetForCheckout(ctx context.Context, userID uuid.UUID) (*cart.Cart, error)
	Clear(ctx context.Context, cartID uuid.UUID) error
}

// StockManager changes product stock atomically.
type StockManager interface {
	DecreaseStock(ctx context.Context, id uuid.UUID, qty int) (*product.Product, error)
	IncreaseStock(ctx context.Context, id uuid.UUID, qty int) error
}

// CacheInvalidator drops cached product data after stock changed.
type CacheInvalidator interface {
	InvalidateCache(ctx context.Context, ids ...uuid.UUID)
}

// Transactor runs a function inside one database transaction.
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	repo  Repository
	carts CartStore
	stock StockManager
	cache CacheInvalidator
	tx    Transactor
}

func NewService(repo Repository, carts CartStore, stock StockManager, cache CacheInvalidator, tx Transactor) *Service {
	return &Service{repo: repo, carts: carts, stock: stock, cache: cache, tx: tx}
}

// Create converts the user's cart into an order.
//
// Everything runs in one transaction: reserving stock for every item,
// inserting the order with its item snapshots, and clearing the cart. If any
// step fails (for example one product is out of stock) the whole checkout is
// rolled back and no stock is lost.
//
// Prices and names come from the row returned by the stock update itself,
// never from the request, so the client cannot influence what it pays.
func (s *Service) Create(ctx context.Context, userID uuid.UUID) (*Order, error) {
	var created *Order
	var productIDs []uuid.UUID

	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		c, err := s.carts.GetForCheckout(ctx, userID)
		if err != nil {
			return err
		}
		if len(c.Items) == 0 {
			return apperror.BadRequest("Cart is empty")
		}

		// Always lock product rows in the same order. Two checkouts that
		// share products would otherwise be able to deadlock each other.
		items := append([]cart.Item(nil), c.Items...)
		sort.Slice(items, func(i, j int) bool {
			return items[i].ProductID.String() < items[j].ProductID.String()
		})

		order := &Order{
			UserID: userID,
			Status: StatusPending,
			Items:  make([]Item, 0, len(items)),
		}
		for _, it := range items {
			p, err := s.stock.DecreaseStock(ctx, it.ProductID, it.Quantity)
			if errors.Is(err, product.ErrInsufficientStock) {
				name := it.Product.Name
				if name == "" {
					name = "A product in your cart"
				}
				return apperror.Conflict(fmt.Sprintf("%s is out of stock or no longer available in the requested quantity", name))
			}
			if err != nil {
				return err
			}

			productID := p.ID
			subtotal := p.Price * int64(it.Quantity)
			order.Items = append(order.Items, Item{
				ProductID:   &productID,
				ProductName: p.Name,
				UnitPrice:   p.Price,
				Quantity:    it.Quantity,
				Subtotal:    subtotal,
			})
			order.TotalAmount += subtotal
			productIDs = append(productIDs, p.ID)
		}

		number, err := newOrderNumber()
		if err != nil {
			return err
		}
		order.OrderNumber = number

		if err := s.repo.Create(ctx, order); err != nil {
			return err
		}
		if err := s.carts.Clear(ctx, c.ID); err != nil {
			return err
		}
		created = order
		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}

	// Only after a successful commit: cached products now show stale stock.
	s.cache.InvalidateCache(ctx, productIDs...)
	return created, nil
}

// Get returns an order the caller is allowed to see. A customer asking for
// someone else's order gets 404 rather than 403, so the API does not confirm
// that the order exists.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) (*Order, error) {
	o, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) || (err == nil && !isAdmin && o.UserID != userID) {
		return nil, apperror.NotFound("Order not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return o, nil
}

// List returns the caller's own orders, or all orders for an admin.
func (s *Service) List(ctx context.Context, userID uuid.UUID, isAdmin bool, offset, limit int) ([]Order, int64, error) {
	var filter *uuid.UUID
	if !isAdmin {
		filter = &userID
	}
	orders, total, err := s.repo.List(ctx, filter, offset, limit)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return orders, total, nil
}

// Cancel cancels a PENDING order and returns its items to stock, atomically.
// Paid orders cannot be cancelled here because that would require a refund
// flow, which is out of scope.
func (s *Service) Cancel(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) (*Order, error) {
	o, err := s.Get(ctx, userID, isAdmin, id)
	if err != nil {
		return nil, err
	}

	var productIDs []uuid.UUID
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// The compare-and-swap guarantees stock is restored exactly once,
		// even if two cancel requests (or a cancel and a payment) race.
		ok, err := s.repo.TransitionStatus(ctx, o.ID, StatusPending, StatusCancelled)
		if err != nil {
			return err
		}
		if !ok {
			return apperror.Conflict("Only orders with status PENDING can be cancelled")
		}
		for _, it := range o.Items {
			if it.ProductID == nil {
				continue
			}
			if err := s.stock.IncreaseStock(ctx, *it.ProductID, it.Quantity); err != nil {
				return err
			}
			productIDs = append(productIDs, *it.ProductID)
		}
		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}

	s.cache.InvalidateCache(ctx, productIDs...)
	return s.Get(ctx, userID, isAdmin, id)
}

// UpdateStatus moves an order along the fulfilment state machine (admin).
func (s *Service) UpdateStatus(ctx context.Context, adminID, id uuid.UUID, req UpdateStatusRequest) (*Order, error) {
	target := Status(strings.ToUpper(req.Status))

	o, err := s.Get(ctx, adminID, true, id)
	if err != nil {
		return nil, err
	}
	if !CanTransition(o.Status, target) {
		return nil, apperror.Conflict(fmt.Sprintf("Order status cannot change from %s to %s", o.Status, target))
	}
	if target == StatusCancelled {
		return s.Cancel(ctx, adminID, true, id)
	}

	ok, err := s.repo.TransitionStatus(ctx, id, o.Status, target)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !ok {
		return nil, apperror.Conflict("Order status was changed by another request, please reload")
	}
	return s.Get(ctx, adminID, true, id)
}

// wrap keeps business errors as they are and hides everything else behind a
// generic 500.
func wrap(err error) error {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return apperror.Internal(err)
}

func newOrderNumber() (string, error) {
	suffix, err := utils.RandomHex(4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ORD-%s-%s", time.Now().UTC().Format("20060102"), strings.ToUpper(suffix)), nil
}
