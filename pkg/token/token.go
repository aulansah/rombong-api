// Package token issues and verifies the JWT access tokens.
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrInvalidToken is returned for any token that fails verification.
// The specific reason is deliberately not exposed to the caller.
var ErrInvalidToken = errors.New("invalid or expired token")

// Claims is the access token payload. The user ID lives in the standard
// "sub" claim; the role is the only custom claim.
type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// UserID parses the subject claim.
func (c *Claims) UserID() (uuid.UUID, error) {
	return uuid.Parse(c.Subject)
}

// Manager signs and verifies access tokens with HMAC-SHA256.
type Manager struct {
	secret []byte
	ttl    time.Duration
	issuer string
}

func NewManager(secret string, ttl time.Duration, issuer string) *Manager {
	return &Manager{secret: []byte(secret), ttl: ttl, issuer: issuer}
}

// TTL returns the access token lifetime.
func (m *Manager) TTL() time.Duration { return m.ttl }

// Generate issues a signed access token for the given user.
func (m *Manager) Generate(userID uuid.UUID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			ID:        uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// Parse verifies the signature, algorithm, issuer and expiration.
//
// Pinning the accepted algorithm to HS256 blocks the classic "alg: none" and
// algorithm-confusion attacks.
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims,
		func(*jwt.Token) (any, error) { return m.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if _, err := claims.UserID(); err != nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
