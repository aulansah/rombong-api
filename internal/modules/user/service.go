package user

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

// Repository is what the service needs from the persistence layer.
// It is declared here, on the consumer side, so tests can supply a fake.
type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	UpdateName(ctx context.Context, id uuid.UUID, name string) error
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error
	List(ctx context.Context, offset, limit int) ([]User, int64, error)
}

// SessionRevoker invalidates every refresh token of a user. It is implemented
// by the auth module; depending on this interface instead of the auth package
// avoids an import cycle (auth already imports user).
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// Transactor runs a function inside one database transaction.
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	repo       Repository
	sessions   SessionRevoker
	tx         Transactor
	bcryptCost int
}

func NewService(repo Repository, sessions SessionRevoker, tx Transactor, bcryptCost int) *Service {
	return &Service{repo: repo, sessions: sessions, tx: tx, bcryptCost: bcryptCost}
}

func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	u, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.NotFound("User not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return u, nil
}

func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, req UpdateProfileRequest) (*User, error) {
	if _, err := s.GetByID(ctx, id); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateName(ctx, id, strings.TrimSpace(req.Name)); err != nil {
		return nil, apperror.Internal(err)
	}
	return s.GetByID(ctx, id)
}

// ChangePassword verifies the current password, stores the new hash and
// signs the user out everywhere by revoking all refresh tokens. Both writes
// happen in one transaction so a failure cannot leave old sessions alive
// next to a new password.
func (s *Service) ChangePassword(ctx context.Context, id uuid.UUID, req ChangePasswordRequest) error {
	u, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.CurrentPassword)) != nil {
		return apperror.BadRequest("Current password is incorrect")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), s.bcryptCost)
	if err != nil {
		return apperror.Internal(err)
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repo.UpdatePassword(ctx, id, string(hash)); err != nil {
			return err
		}
		return s.sessions.RevokeAllForUser(ctx, id)
	})
	if err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (s *Service) List(ctx context.Context, offset, limit int) ([]User, int64, error) {
	users, total, err := s.repo.List(ctx, offset, limit)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return users, total, nil
}
