package product

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/aulansah/go-ecommerce-api/internal/cache"
	"github.com/aulansah/go-ecommerce-api/pkg/apperror"
)

// fakeRepo is an in-memory Repository that counts database reads, so tests
// can prove whether a request was served from the cache.
type fakeRepo struct {
	products  map[uuid.UUID]*Product
	findCalls int
	listCalls int
}

func newFakeRepo() *fakeRepo { return &fakeRepo{products: map[uuid.UUID]*Product{}} }

func (f *fakeRepo) Create(_ context.Context, p *Product) error {
	p.ID = uuid.New()
	f.products[p.ID] = p
	return nil
}

func (f *fakeRepo) Update(_ context.Context, p *Product) error {
	f.products[p.ID] = p
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.products, id)
	return nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (*Product, error) {
	f.findCalls++
	if p, ok := f.products[id]; ok {
		copied := *p
		return &copied, nil
	}
	return nil, apperror.ErrNotFound
}

func (f *fakeRepo) List(_ context.Context, _ ListFilter) ([]Product, int64, error) {
	f.listCalls++
	out := make([]Product, 0, len(f.products))
	for _, p := range f.products {
		out = append(out, *p)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) SetStock(_ context.Context, id uuid.UUID, stock int) error {
	f.products[id].Stock = stock
	return nil
}

type fakeCategories struct{ exists bool }

func (f fakeCategories) Exists(context.Context, uuid.UUID) (bool, error) { return f.exists, nil }

func newTestService(t *testing.T) (*Service, *fakeRepo, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })

	repo := newFakeRepo()
	svc := NewService(repo, fakeCategories{exists: true}, cache.NewRedisFromClient(client), zap.NewNop(), time.Minute, time.Minute)
	return svc, repo, mr
}

func intPtr(v int) *int { return &v }

func validRequest() SaveRequest {
	return SaveRequest{Name: "Kopi Arabika", Description: "250g", Price: 85000, Stock: intPtr(10), CategoryID: uuid.NewString()}
}

func TestCreate(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	p, err := svc.Create(ctx, validRequest())
	require.NoError(t, err)
	assert.Regexp(t, `^kopi-arabika-[0-9a-f]{6}$`, p.Slug)
	assert.Equal(t, int64(85000), p.Price)
	assert.Equal(t, 10, p.Stock)

	t.Run("unknown category is rejected", func(t *testing.T) {
		svc.categories = fakeCategories{exists: false}
		_, err := svc.Create(ctx, validRequest())

		var appErr *apperror.AppError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, http.StatusBadRequest, appErr.Status)
	})
}

func TestGet_CacheAside(t *testing.T) {
	svc, repo, _ := newTestService(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, validRequest())
	require.NoError(t, err)
	repo.findCalls = 0

	first, err := svc.Get(ctx, created.ID)
	require.NoError(t, err)
	second, err := svc.Get(ctx, created.ID)
	require.NoError(t, err)

	assert.Equal(t, 1, repo.findCalls, "second read must come from Redis")
	assert.Equal(t, first.Name, second.Name)

	t.Run("update invalidates the cached product", func(t *testing.T) {
		req := validRequest()
		req.Price = 99000
		_, err := svc.Update(ctx, created.ID, req)
		require.NoError(t, err)

		got, err := svc.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(99000), got.Price, "stale price must not be served")
	})

	t.Run("delete invalidates the cached product", func(t *testing.T) {
		require.NoError(t, svc.Delete(ctx, created.ID))
		_, err := svc.Get(ctx, created.ID)

		var appErr *apperror.AppError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, http.StatusNotFound, appErr.Status)
	})
}

func TestList_CacheIsVersioned(t *testing.T) {
	svc, repo, _ := newTestService(t)
	ctx := context.Background()
	_, err := svc.Create(ctx, validRequest())
	require.NoError(t, err)

	filter := ListFilter{Page: 1, Limit: 20}
	_, err = svc.List(ctx, filter)
	require.NoError(t, err)
	_, err = svc.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.listCalls, "identical query is served from Redis")

	_, err = svc.List(ctx, ListFilter{Page: 1, Limit: 20, Search: "kopi"})
	require.NoError(t, err)
	assert.Equal(t, 2, repo.listCalls, "a different query has its own cache entry")

	_, err = svc.Create(ctx, validRequest())
	require.NoError(t, err)
	result, err := svc.List(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 3, repo.listCalls, "a write invalidates every cached list")
	assert.Equal(t, int64(2), result.Total)
}

func TestList_NormalizesUnsafeSortInput(t *testing.T) {
	f := normalizeFilter(ListFilter{Sort: "price; DROP TABLE products", Order: "sideways", Page: -1, Limit: 9999})

	assert.Equal(t, "created_at", f.Sort, "unknown sort columns fall back to the default")
	assert.Equal(t, "desc", f.Order)
	assert.Equal(t, 1, f.Page)
	assert.Equal(t, 100, f.Limit)
}

func TestRedisDown_FallsBackToDatabase(t *testing.T) {
	svc, repo, mr := newTestService(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, validRequest())
	require.NoError(t, err)

	mr.Close() // simulate a Redis outage
	repo.findCalls, repo.listCalls = 0, 0

	got, err := svc.Get(ctx, created.ID)
	require.NoError(t, err, "a cache outage must not fail the request")
	assert.Equal(t, created.ID, got.ID)

	_, err = svc.List(ctx, ListFilter{Page: 1, Limit: 20})
	require.NoError(t, err)

	req := validRequest()
	req.Price = 1000
	_, err = svc.Update(ctx, created.ID, req)
	require.NoError(t, err, "writes must still succeed when invalidation fails")

	assert.Equal(t, 2, repo.findCalls)
	assert.Equal(t, 1, repo.listCalls)
}
