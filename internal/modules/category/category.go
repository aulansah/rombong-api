// Package category manages product categories.
package category

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/aulansah/go-ecommerce-api/internal/database"
	"github.com/aulansah/go-ecommerce-api/internal/utils"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

// Category is the categories table.
type Category struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// Request is the body of POST /categories and PUT /categories/:id.
type Request struct {
	Name string `json:"name" validate:"required,min=2,max=100"`
}

// Response is the public representation of a category.
type Response struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ToResponse(c *Category) Response {
	return Response{ID: c.ID, Name: c.Name, Slug: c.Slug, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) Create(ctx context.Context, c *Category) error {
	err := database.FromContext(ctx, r.db).Create(c).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.ErrDuplicate
	}
	return err
}

func (r *GormRepository) Update(ctx context.Context, c *Category) error {
	err := database.FromContext(ctx, r.db).Model(c).
		Select("name", "slug").Updates(c).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.ErrDuplicate
	}
	return err
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return database.FromContext(ctx, r.db).Delete(&Category{}, "id = ?", id).Error
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (*Category, error) {
	var c Category
	err := database.FromContext(ctx, r.db).First(&c, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &c, err
}

func (r *GormRepository) List(ctx context.Context) ([]Category, error) {
	var out []Category
	err := database.FromContext(ctx, r.db).Order("name ASC").Find(&out).Error
	return out, err
}

// HasProducts reports whether any active product still uses the category.
// It queries the products table by name to avoid importing the product
// package (product already depends on category).
func (r *GormRepository) HasProducts(ctx context.Context, id uuid.UUID) (bool, error) {
	var count int64
	err := database.FromContext(ctx, r.db).Table("products").
		Where("category_id = ? AND deleted_at IS NULL", id).
		Limit(1).Count(&count).Error
	return count > 0, err
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// Repository is what the service needs from the persistence layer.
type Repository interface {
	Create(ctx context.Context, c *Category) error
	Update(ctx context.Context, c *Category) error
	Delete(ctx context.Context, id uuid.UUID) error
	FindByID(ctx context.Context, id uuid.UUID) (*Category, error)
	List(ctx context.Context) ([]Category, error)
	HasProducts(ctx context.Context, id uuid.UUID) (bool, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) Create(ctx context.Context, req Request) (*Category, error) {
	name := strings.TrimSpace(req.Name)
	c := &Category{Name: name, Slug: utils.Slugify(name)}
	if c.Slug == "" {
		return nil, apperror.BadRequest("Category name must contain letters or digits")
	}
	if err := s.repo.Create(ctx, c); err != nil {
		if errors.Is(err, apperror.ErrDuplicate) {
			return nil, apperror.Conflict("A category with this name already exists")
		}
		return nil, apperror.Internal(err)
	}
	return c, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Category, error) {
	c, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.NotFound("Category not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return c, nil
}

// Exists is used by the product module to validate a category reference.
func (s *Service) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) List(ctx context.Context) ([]Category, error) {
	out, err := s.repo.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req Request) (*Category, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	c.Name = strings.TrimSpace(req.Name)
	c.Slug = utils.Slugify(c.Name)
	if c.Slug == "" {
		return nil, apperror.BadRequest("Category name must contain letters or digits")
	}
	if err := s.repo.Update(ctx, c); err != nil {
		if errors.Is(err, apperror.ErrDuplicate) {
			return nil, apperror.Conflict("A category with this name already exists")
		}
		return nil, apperror.Internal(err)
	}
	return s.Get(ctx, id)
}

// Delete soft-deletes a category. A category that still has products cannot
// be deleted, otherwise those products would point at a hidden category.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	used, err := s.repo.HasProducts(ctx, id)
	if err != nil {
		return apperror.Internal(err)
	}
	if used {
		return apperror.Conflict("Category still has products and cannot be deleted")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return apperror.Internal(err)
	}
	return nil
}
