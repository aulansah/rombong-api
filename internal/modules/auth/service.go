package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/aulansah/rombong-api/internal/middleware"
	"github.com/aulansah/rombong-api/internal/modules/user"
	"github.com/aulansah/rombong-api/pkg/apperror"
	"github.com/aulansah/rombong-api/pkg/token"
)

// UserStore is the part of the user repository auth needs.
type UserStore interface {
	Create(ctx context.Context, u *user.User) error
	FindByID(ctx context.Context, id uuid.UUID) (*user.User, error)
	FindByEmail(ctx context.Context, email string) (*user.User, error)
}

// TokenStore persists refresh tokens.
type TokenStore interface {
	Create(ctx context.Context, t *RefreshToken) error
	FindByHash(ctx context.Context, hash string) (*RefreshToken, error)
	Revoke(ctx context.Context, id uuid.UUID) (bool, error)
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

type Service struct {
	users      UserStore
	tokens     TokenStore
	jwt        *token.Manager
	refreshTTL time.Duration
	bcryptCost int
}

func NewService(users UserStore, tokens TokenStore, jwt *token.Manager, refreshTTL time.Duration, bcryptCost int) *Service {
	return &Service{users: users, tokens: tokens, jwt: jwt, refreshTTL: refreshTTL, bcryptCost: bcryptCost}
}

// errInvalidCredentials is identical for "unknown email" and "wrong
// password" so the API does not reveal which emails are registered.
var errInvalidCredentials = apperror.Unauthorized("Invalid email or password")

// Register creates a customer account. The role is never taken from input.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*user.User, error) {
	return s.createUser(ctx, req.Name, req.Email, req.Password, middleware.RoleCustomer)
}

// EnsureAdmin creates the seed admin account if it does not exist yet.
func (s *Service) EnsureAdmin(ctx context.Context, name, email, password string) error {
	_, err := s.users.FindByEmail(ctx, normalizeEmail(email))
	if err == nil {
		return nil
	}
	if !errors.Is(err, apperror.ErrNotFound) {
		return err
	}
	_, err = s.createUser(ctx, name, email, password, middleware.RoleAdmin)
	return err
}

func (s *Service) createUser(ctx context.Context, name, email, password, role string) (*user.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	u := &user.User{
		Name:         strings.TrimSpace(name),
		Email:        normalizeEmail(email),
		PasswordHash: string(hash),
		Role:         role,
	}
	// The unique index is the real guard against duplicates; checking first
	// with a SELECT would still race with a concurrent registration.
	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, apperror.ErrDuplicate) {
			return nil, apperror.Conflict("Email is already registered")
		}
		return nil, apperror.Internal(err)
	}
	return u, nil
}

// Login verifies the credentials and issues a token pair.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	u, err := s.users.FindByEmail(ctx, normalizeEmail(req.Email))
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, errInvalidCredentials
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		return nil, errInvalidCredentials
	}

	tokens, err := s.issueTokens(ctx, u)
	if err != nil {
		return nil, err
	}
	return &LoginResponse{User: user.ToResponse(u), Tokens: *tokens}, nil
}

// Refresh rotates the refresh token: the presented token is revoked and a
// new pair is issued.
//
// If a token that was already revoked is presented again, it was either
// replayed by an attacker or by the legitimate user after theft. Both cases
// are handled the same way: every session of that user is revoked.
func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (*TokenResponse, error) {
	invalid := apperror.Unauthorized("Invalid or expired refresh token")

	stored, err := s.tokens.FindByHash(ctx, hashToken(req.RefreshToken))
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, invalid
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if time.Now().After(stored.ExpiresAt) {
		return nil, invalid
	}

	revoked, err := s.tokens.Revoke(ctx, stored.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !revoked {
		if err := s.tokens.RevokeAllForUser(ctx, stored.UserID); err != nil {
			return nil, apperror.Internal(err)
		}
		return nil, invalid
	}

	u, err := s.users.FindByID(ctx, stored.UserID)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, invalid
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return s.issueTokens(ctx, u)
}

// Logout revokes the presented refresh token. It is idempotent and only
// touches tokens that belong to the authenticated user.
func (s *Service) Logout(ctx context.Context, userID uuid.UUID, req RefreshRequest) error {
	stored, err := s.tokens.FindByHash(ctx, hashToken(req.RefreshToken))
	if errors.Is(err, apperror.ErrNotFound) {
		return nil
	}
	if err != nil {
		return apperror.Internal(err)
	}
	if stored.UserID != userID {
		return nil
	}
	if _, err := s.tokens.Revoke(ctx, stored.ID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (s *Service) issueTokens(ctx context.Context, u *user.User) (*TokenResponse, error) {
	access, err := s.jwt.Generate(u.ID, u.Role)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, apperror.Internal(err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(raw)

	err = s.tokens.Create(ctx, &RefreshToken{
		UserID:    u.ID,
		TokenHash: hashToken(refresh),
		ExpiresAt: time.Now().UTC().Add(s.refreshTTL),
	})
	if err != nil {
		return nil, apperror.Internal(err)
	}

	return &TokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.jwt.TTL().Seconds()),
	}, nil
}

// hashToken uses plain SHA-256. A slow hash like bcrypt is unnecessary here
// because the token is 256 bits of randomness, not a guessable password.
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
