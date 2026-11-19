// Package auth implements registration, login and the refresh token flow.
package auth

import (
	"time"

	"github.com/google/uuid"

	"github.com/aulansah/rombong-api/internal/modules/user"
)

// RefreshToken is the refresh_tokens table. Only the SHA-256 hash of the
// token is stored, so a database leak does not hand out usable sessions.
type RefreshToken struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// RegisterRequest is the body of POST /auth/register.
type RegisterRequest struct {
	Name     string `json:"name" validate:"required,min=2,max=100"`
	Email    string `json:"email" validate:"required,email,max=255"`
	Password string `json:"password" validate:"required,password"`
}

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

// RefreshRequest is the body of POST /auth/refresh and POST /auth/logout.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// TokenResponse is returned by login and refresh.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// LoginResponse is returned by POST /auth/login.
type LoginResponse struct {
	User   user.Response `json:"user"`
	Tokens TokenResponse `json:"tokens"`
}
