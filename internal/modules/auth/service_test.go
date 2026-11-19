package auth

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/aulansah/rombong-api/internal/modules/user"
	"github.com/aulansah/rombong-api/pkg/apperror"
	"github.com/aulansah/rombong-api/pkg/token"
)

// ---------------------------------------------------------------------------
// In-memory fakes. The service depends on small interfaces, so no database
// or mocking library is needed for unit tests.
// ---------------------------------------------------------------------------

type fakeUsers struct {
	mu      sync.Mutex
	byEmail map[string]*user.User
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byEmail: map[string]*user.User{}} }

func (f *fakeUsers) Create(_ context.Context, u *user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byEmail[u.Email]; ok {
		return apperror.ErrDuplicate
	}
	u.ID = uuid.New()
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return nil, apperror.ErrNotFound
}

func (f *fakeUsers) FindByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, apperror.ErrNotFound
}

type fakeTokens struct {
	mu     sync.Mutex
	byHash map[string]*RefreshToken
}

func newFakeTokens() *fakeTokens { return &fakeTokens{byHash: map[string]*RefreshToken{}} }

func (f *fakeTokens) Create(_ context.Context, t *RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = uuid.New()
	f.byHash[t.TokenHash] = t
	return nil
}

func (f *fakeTokens) FindByHash(_ context.Context, hash string) (*RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.byHash[hash]; ok {
		copied := *t
		return &copied, nil
	}
	return nil, apperror.ErrNotFound
}

func (f *fakeTokens) Revoke(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.byHash {
		if t.ID == id && t.RevokedAt == nil {
			now := time.Now()
			t.RevokedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeTokens) RevokeAllForUser(_ context.Context, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, t := range f.byHash {
		if t.UserID == userID && t.RevokedAt == nil {
			t.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeTokens) activeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.byHash {
		if t.RevokedAt == nil {
			n++
		}
	}
	return n
}

func newTestService() (*Service, *fakeUsers, *fakeTokens) {
	users, tokens := newFakeUsers(), newFakeTokens()
	jwt := token.NewManager("unit-test-secret-with-at-least-32-chars", time.Minute, "test")
	return NewService(users, tokens, jwt, time.Hour, bcrypt.MinCost), users, tokens
}

func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, status, appErr.Status)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRegister(t *testing.T) {
	svc, users, _ := newTestService()
	ctx := context.Background()

	u, err := svc.Register(ctx, RegisterRequest{Name: " Auldy ", Email: "  Auldy@Example.com ", Password: "Secret123"})
	require.NoError(t, err)

	assert.Equal(t, "Auldy", u.Name)
	assert.Equal(t, "auldy@example.com", u.Email, "email is normalized")
	assert.Equal(t, "customer", u.Role, "public registration never creates admins")
	assert.NotEqual(t, "Secret123", u.PasswordHash, "password must not be stored in plaintext")
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("Secret123")))
	assert.Len(t, users.byEmail, 1)

	t.Run("duplicate email is rejected regardless of case", func(t *testing.T) {
		_, err := svc.Register(ctx, RegisterRequest{Name: "Other", Email: "AULDY@example.com", Password: "Secret123"})
		requireStatus(t, err, http.StatusConflict)
	})
}

func TestLogin(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()
	_, err := svc.Register(ctx, RegisterRequest{Name: "Auldy", Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		res, err := svc.Login(ctx, LoginRequest{Email: "auldy@example.com", Password: "Secret123"})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Tokens.AccessToken)
		assert.NotEmpty(t, res.Tokens.RefreshToken)
		assert.Equal(t, "Bearer", res.Tokens.TokenType)
		assert.Equal(t, int64(60), res.Tokens.ExpiresIn)
	})

	t.Run("wrong password and unknown email return the same error", func(t *testing.T) {
		_, errPassword := svc.Login(ctx, LoginRequest{Email: "auldy@example.com", Password: "Wrong1234"})
		_, errEmail := svc.Login(ctx, LoginRequest{Email: "nobody@example.com", Password: "Secret123"})

		requireStatus(t, errPassword, http.StatusUnauthorized)
		requireStatus(t, errEmail, http.StatusUnauthorized)
		assert.Equal(t, errPassword.Error(), errEmail.Error(), "must not reveal which emails exist")
	})
}

func TestRefresh_RotatesToken(t *testing.T) {
	svc, _, tokens := newTestService()
	ctx := context.Background()
	_, err := svc.Register(ctx, RegisterRequest{Name: "Auldy", Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)
	login, err := svc.Login(ctx, LoginRequest{Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)

	rotated, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: login.Tokens.RefreshToken})
	require.NoError(t, err)
	assert.NotEqual(t, login.Tokens.RefreshToken, rotated.RefreshToken)
	assert.Equal(t, 1, tokens.activeCount(), "the old token is revoked, the new one is active")

	t.Run("reusing the old token revokes every session", func(t *testing.T) {
		_, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: login.Tokens.RefreshToken})
		requireStatus(t, err, http.StatusUnauthorized)
		assert.Equal(t, 0, tokens.activeCount())

		_, err = svc.Refresh(ctx, RefreshRequest{RefreshToken: rotated.RefreshToken})
		requireStatus(t, err, http.StatusUnauthorized)
	})
}

func TestRefresh_RejectsUnknownAndExpiredTokens(t *testing.T) {
	svc, _, tokens := newTestService()
	ctx := context.Background()

	_, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: "does-not-exist"})
	requireStatus(t, err, http.StatusUnauthorized)

	_, err = svc.Register(ctx, RegisterRequest{Name: "Auldy", Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)
	login, err := svc.Login(ctx, LoginRequest{Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)

	tokens.mu.Lock()
	tokens.byHash[hashToken(login.Tokens.RefreshToken)].ExpiresAt = time.Now().Add(-time.Minute)
	tokens.mu.Unlock()

	_, err = svc.Refresh(ctx, RefreshRequest{RefreshToken: login.Tokens.RefreshToken})
	requireStatus(t, err, http.StatusUnauthorized)
}

func TestLogout(t *testing.T) {
	svc, users, tokens := newTestService()
	ctx := context.Background()
	_, err := svc.Register(ctx, RegisterRequest{Name: "Auldy", Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)
	login, err := svc.Login(ctx, LoginRequest{Email: "auldy@example.com", Password: "Secret123"})
	require.NoError(t, err)
	owner := users.byEmail["auldy@example.com"].ID

	t.Run("another user cannot revoke the token", func(t *testing.T) {
		require.NoError(t, svc.Logout(ctx, uuid.New(), RefreshRequest{RefreshToken: login.Tokens.RefreshToken}))
		assert.Equal(t, 1, tokens.activeCount())
	})

	t.Run("owner revokes the token and logout is idempotent", func(t *testing.T) {
		require.NoError(t, svc.Logout(ctx, owner, RefreshRequest{RefreshToken: login.Tokens.RefreshToken}))
		require.NoError(t, svc.Logout(ctx, owner, RefreshRequest{RefreshToken: login.Tokens.RefreshToken}))
		assert.Equal(t, 0, tokens.activeCount())

		_, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: login.Tokens.RefreshToken})
		requireStatus(t, err, http.StatusUnauthorized)
	})
}
