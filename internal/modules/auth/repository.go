package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/aulansah/go-ecommerce-api/internal/database"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

// GormRepository stores refresh tokens in PostgreSQL.
type GormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) Create(ctx context.Context, t *RefreshToken) error {
	return database.FromContext(ctx, r.db).Create(t).Error
}

func (r *GormRepository) FindByHash(ctx context.Context, hash string) (*RefreshToken, error) {
	var t RefreshToken
	err := database.FromContext(ctx, r.db).First(&t, "token_hash = ?", hash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &t, err
}

// Revoke marks a token as revoked only if it is still active. The boolean
// tells the caller whether this call was the one that revoked it, which is
// what makes refresh token rotation safe under concurrent requests.
func (r *GormRepository) Revoke(ctx context.Context, id uuid.UUID) (bool, error) {
	res := database.FromContext(ctx, r.db).Model(&RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", time.Now().UTC())
	return res.RowsAffected == 1, res.Error
}

// RevokeAllForUser signs the user out of every session.
func (r *GormRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	return database.FromContext(ctx, r.db).Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now().UTC()).Error
}
