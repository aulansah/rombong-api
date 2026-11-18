package token

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "unit-test-secret-with-at-least-32-chars"

func TestManager_GenerateAndParse(t *testing.T) {
	m := NewManager(testSecret, time.Minute, "test-issuer")
	userID := uuid.New()

	signed, err := m.Generate(userID, "admin")
	require.NoError(t, err)

	claims, err := m.Parse(signed)
	require.NoError(t, err)

	got, err := claims.UserID()
	require.NoError(t, err)
	assert.Equal(t, userID, got)
	assert.Equal(t, "admin", claims.Role)
}

func TestManager_Parse_RejectsInvalidTokens(t *testing.T) {
	m := NewManager(testSecret, time.Minute, "test-issuer")
	valid, err := m.Generate(uuid.New(), "customer")
	require.NoError(t, err)

	expired, err := NewManager(testSecret, -time.Minute, "test-issuer").Generate(uuid.New(), "customer")
	require.NoError(t, err)

	otherSecret, err := NewManager("another-secret-with-at-least-32-chars!", time.Minute, "test-issuer").Generate(uuid.New(), "customer")
	require.NoError(t, err)

	otherIssuer, err := NewManager(testSecret, time.Minute, "someone-else").Generate(uuid.New(), "customer")
	require.NoError(t, err)

	// A token that declares "alg: none" must never be accepted.
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			Issuer:    "test-issuer",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	cases := map[string]string{
		"empty":            "",
		"garbage":          "not-a-jwt",
		"tampered":         valid[:len(valid)-2] + "xx",
		"expired":          expired,
		"wrong secret":     otherSecret,
		"wrong issuer":     otherIssuer,
		"alg none":         unsigned,
		"truncated header": valid[10:],
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.Parse(tok)
			assert.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}
