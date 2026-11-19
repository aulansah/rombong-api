package product

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/aulansah/rombong-api/internal/cache"
	"github.com/aulansah/rombong-api/internal/utils"
	"github.com/aulansah/rombong-api/pkg/apperror"
)

// Repository is what the service needs from the persistence layer.
type Repository interface {
	Create(ctx context.Context, p *Product) error
	Update(ctx context.Context, p *Product) error
	Delete(ctx context.Context, id uuid.UUID) error
	FindByID(ctx context.Context, id uuid.UUID) (*Product, error)
	List(ctx context.Context, f ListFilter) ([]Product, int64, error)
	SetStock(ctx context.Context, id uuid.UUID, stock int) error
}

// CategoryChecker validates a category reference.
type CategoryChecker interface {
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
}

// allowedSorts whitelists the sortable columns (see Repository.List).
var allowedSorts = map[string]bool{"created_at": true, "price": true, "name": true}

const listVersionKey = "products:list:version"

type Service struct {
	repo       Repository
	categories CategoryChecker
	cache      cache.Cache
	log        *zap.Logger
	detailTTL  time.Duration
	listTTL    time.Duration
}

func NewService(repo Repository, categories CategoryChecker, c cache.Cache, log *zap.Logger, detailTTL, listTTL time.Duration) *Service {
	return &Service{repo: repo, categories: categories, cache: c, log: log, detailTTL: detailTTL, listTTL: listTTL}
}

func (s *Service) Create(ctx context.Context, req SaveRequest) (*Response, error) {
	categoryID, err := s.resolveCategory(ctx, req.CategoryID)
	if err != nil {
		return nil, err
	}

	// A random suffix keeps slugs unique without a retry loop, so two
	// products may share the same name.
	suffix, err := utils.RandomHex(3)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	name := strings.TrimSpace(req.Name)
	p := &Product{
		CategoryID:  categoryID,
		Name:        name,
		Slug:        strings.Trim(utils.Slugify(name)+"-"+suffix, "-"),
		Description: strings.TrimSpace(req.Description),
		Price:       req.Price,
		Stock:       *req.Stock,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		if errors.Is(err, apperror.ErrDuplicate) {
			return nil, apperror.Conflict("Product slug already exists, please retry")
		}
		return nil, apperror.Internal(err)
	}

	s.invalidateLists(ctx)
	res := ToResponse(p)
	return &res, nil
}

// Get returns a product using the cache-aside strategy:
//
//  1. Try Redis.
//  2. On a miss (or any Redis error) read PostgreSQL.
//  3. Store the result in Redis with a TTL for the next reader.
//
// Redis errors are logged and otherwise ignored: the cache may only ever make
// a request faster, never make it fail.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Response, error) {
	key := detailKey(id)

	var cached Response
	found, err := s.cache.Get(ctx, key, &cached)
	if err != nil {
		s.log.Warn("product cache read failed", zap.Error(err))
	}
	if found {
		return &cached, nil
	}

	p, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.NotFound("Product not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}

	res := ToResponse(p)
	if err := s.cache.Set(ctx, key, res, s.detailTTL); err != nil {
		s.log.Warn("product cache write failed", zap.Error(err))
	}
	return &res, nil
}

// List returns one page of products, cached per distinct query.
//
// List keys cannot be invalidated one by one (there is one per filter
// combination), so every key embeds a version number. Any write bumps the
// version with INCR, which makes all old list keys unreachable; they then
// expire on their own through the TTL.
func (s *Service) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	f = normalizeFilter(f)
	key, cacheable := s.listKey(ctx, f)

	if cacheable {
		var cached ListResult
		found, err := s.cache.Get(ctx, key, &cached)
		if err != nil {
			s.log.Warn("product list cache read failed", zap.Error(err))
		}
		if found {
			return &cached, nil
		}
	}

	products, total, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	result := &ListResult{Items: make([]Response, 0, len(products)), Total: total}
	for i := range products {
		result.Items = append(result.Items, ToResponse(&products[i]))
	}

	if cacheable {
		if err := s.cache.Set(ctx, key, result, s.listTTL); err != nil {
			s.log.Warn("product list cache write failed", zap.Error(err))
		}
	}
	return result, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req SaveRequest) (*Response, error) {
	p, err := s.repo.FindByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, apperror.NotFound("Product not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}

	categoryID, err := s.resolveCategory(ctx, req.CategoryID)
	if err != nil {
		return nil, err
	}

	// The slug is intentionally left unchanged: it is part of public URLs.
	p.CategoryID = categoryID
	p.Name = strings.TrimSpace(req.Name)
	p.Description = strings.TrimSpace(req.Description)
	p.Price = req.Price
	p.Stock = *req.Stock
	if err := s.repo.Update(ctx, p); err != nil {
		return nil, apperror.Internal(err)
	}

	s.InvalidateCache(ctx, id)
	res := ToResponse(p)
	return &res, nil
}

func (s *Service) UpdateStock(ctx context.Context, id uuid.UUID, req StockRequest) (*Response, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			return nil, apperror.NotFound("Product not found")
		}
		return nil, apperror.Internal(err)
	}
	if err := s.repo.SetStock(ctx, id, *req.Stock); err != nil {
		return nil, apperror.Internal(err)
	}
	s.InvalidateCache(ctx, id)
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			return apperror.NotFound("Product not found")
		}
		return apperror.Internal(err)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return apperror.Internal(err)
	}
	s.InvalidateCache(ctx, id)
	return nil
}

// InvalidateCache removes the cached details of the given products and
// invalidates every cached list. It is called after any write, including
// stock changes made by the order module.
//
// Invalidation happens after the database write. If Redis is down at that
// moment the stale entry survives until its TTL, which is the accepted
// trade-off of cache-aside: bounded staleness instead of failed writes.
func (s *Service) InvalidateCache(ctx context.Context, ids ...uuid.UUID) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, detailKey(id))
	}
	if err := s.cache.Delete(ctx, keys...); err != nil {
		s.log.Warn("product cache invalidation failed", zap.Error(err))
	}
	s.invalidateLists(ctx)
}

func (s *Service) invalidateLists(ctx context.Context) {
	if _, err := s.cache.Incr(ctx, listVersionKey); err != nil {
		s.log.Warn("product list cache invalidation failed", zap.Error(err))
	}
}

// listKey builds the versioned cache key. When the version cannot be read
// the list is simply not cached for this request.
func (s *Service) listKey(ctx context.Context, f ListFilter) (string, bool) {
	var version int64
	if _, err := s.cache.Get(ctx, listVersionKey, &version); err != nil {
		s.log.Warn("product list version read failed", zap.Error(err))
		return "", false
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return "", false
	}
	sum := sha1.Sum(raw)
	return fmt.Sprintf("products:list:v%d:%s", version, hex.EncodeToString(sum[:])), true
}

func (s *Service) resolveCategory(ctx context.Context, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperror.BadRequest("Invalid category_id")
	}
	ok, err := s.categories.Exists(ctx, id)
	if err != nil {
		return uuid.Nil, apperror.Internal(err)
	}
	if !ok {
		return uuid.Nil, apperror.BadRequest("Category does not exist")
	}
	return id, nil
}

func normalizeFilter(f ListFilter) ListFilter {
	f.Search = strings.TrimSpace(f.Search)
	f.Sort = strings.ToLower(f.Sort)
	if !allowedSorts[f.Sort] {
		f.Sort = "created_at"
	}
	f.Order = strings.ToLower(f.Order)
	if f.Order != "asc" && f.Order != "desc" {
		f.Order = "desc"
	}
	p := utils.NewPagination(f.Page, f.Limit)
	f.Page, f.Limit = p.Page, p.Limit
	return f
}

func detailKey(id uuid.UUID) string { return "product:" + id.String() }
