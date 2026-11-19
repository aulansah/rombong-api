package cart

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/aulansah/rombong-api/internal/modules/product"
	"github.com/aulansah/rombong-api/pkg/apperror"
)

// Repository is what the service needs from the persistence layer.
type Repository interface {
	GetOrCreate(ctx context.Context, userID uuid.UUID) (*Cart, error)
	FindItem(ctx context.Context, cartID, itemID uuid.UUID) (*Item, error)
	UpsertItem(ctx context.Context, cartID, productID uuid.UUID, quantity int) error
	UpdateItemQuantity(ctx context.Context, itemID uuid.UUID, quantity int) error
	DeleteItem(ctx context.Context, itemID uuid.UUID) error
	Clear(ctx context.Context, cartID uuid.UUID) error
}

// ProductReader reads products straight from the database. The cart must not
// use the cached product: stock decisions need fresh data.
type ProductReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*product.Product, error)
}

type Service struct {
	repo     Repository
	products ProductReader
}

func NewService(repo Repository, products ProductReader) *Service {
	return &Service{repo: repo, products: products}
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	c, err := s.repo.GetOrCreate(ctx, userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return c, nil
}

// AddItem adds a product to the cart. Adding a product that is already in
// the cart increases its quantity. The resulting quantity may not exceed the
// available stock.
//
// This check is a courtesy to the user, not the oversell protection: stock
// can still change before checkout, where it is enforced atomically.
func (s *Service) AddItem(ctx context.Context, userID uuid.UUID, req AddItemRequest) (*Cart, error) {
	productID, err := uuid.Parse(req.ProductID)
	if err != nil {
		return nil, apperror.BadRequest("Invalid product_id")
	}

	p, err := s.products.FindByID(ctx, productID)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.NotFound("Product not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if p.Stock <= 0 {
		return nil, apperror.Conflict("Product is out of stock")
	}

	c, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}

	quantity := req.Quantity
	for _, it := range c.Items {
		if it.ProductID == productID {
			quantity += it.Quantity
			break
		}
	}
	if quantity > p.Stock {
		return nil, apperror.Conflict(fmt.Sprintf("Only %d unit(s) of this product are available", p.Stock))
	}

	if err := s.repo.UpsertItem(ctx, c.ID, productID, quantity); err != nil {
		return nil, apperror.Internal(err)
	}
	return s.Get(ctx, userID)
}

// UpdateItem sets a new quantity for an item that belongs to the user's cart.
func (s *Service) UpdateItem(ctx context.Context, userID, itemID uuid.UUID, req UpdateItemRequest) (*Cart, error) {
	c, item, err := s.ownedItem(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}

	p, err := s.products.FindByID(ctx, item.ProductID)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.Conflict("Product is no longer available, please remove it from the cart")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if req.Quantity > p.Stock {
		return nil, apperror.Conflict(fmt.Sprintf("Only %d unit(s) of this product are available", p.Stock))
	}

	if err := s.repo.UpdateItemQuantity(ctx, item.ID, req.Quantity); err != nil {
		return nil, apperror.Internal(err)
	}
	return s.Get(ctx, c.UserID)
}

func (s *Service) RemoveItem(ctx context.Context, userID, itemID uuid.UUID) (*Cart, error) {
	c, item, err := s.ownedItem(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.DeleteItem(ctx, item.ID); err != nil {
		return nil, apperror.Internal(err)
	}
	return s.Get(ctx, c.UserID)
}

func (s *Service) Clear(ctx context.Context, userID uuid.UUID) error {
	c, err := s.Get(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.repo.Clear(ctx, c.ID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// ownedItem loads an item through the caller's own cart. An item ID from
// another user's cart is simply "not found", so IDs cannot be probed.
func (s *Service) ownedItem(ctx context.Context, userID, itemID uuid.UUID) (*Cart, *Item, error) {
	c, err := s.Get(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	item, err := s.repo.FindItem(ctx, c.ID, itemID)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, nil, apperror.NotFound("Cart item not found")
	}
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	return c, item, nil
}
