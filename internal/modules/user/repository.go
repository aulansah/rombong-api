package user

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/aulansah/go-ecommerce-api/internal/database"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

// GormRepository is the PostgreSQL implementation of the user store.
type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) Create(ctx context.Context, u *User) error {
	err := database.FromContext(ctx, r.db).Create(u).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.ErrDuplicate
	}
	return err
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := database.FromContext(ctx, r.db).First(&u, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &u, err
}

func (r *GormRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := database.FromContext(ctx, r.db).First(&u, "email = ?", email).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &u, err
}

// UpdateName changes only the name column.
func (r *GormRepository) UpdateName(ctx context.Context, id uuid.UUID, name string) error {
	return database.FromContext(ctx, r.db).Model(&User{}).Where("id = ?", id).Update("name", name).Error
}

// UpdatePassword changes only the password hash column.
func (r *GormRepository) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	return database.FromContext(ctx, r.db).Model(&User{}).Where("id = ?", id).Update("password_hash", hash).Error
}

func (r *GormRepository) List(ctx context.Context, offset, limit int) ([]User, int64, error) {
	db := database.FromContext(ctx, r.db)

	var total int64
	if err := db.Model(&User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []User
	err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&users).Error
	return users, total, err
}
