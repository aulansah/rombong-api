package middleware

import (
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/aulansah/rombong-api/internal/cache"
	"github.com/aulansah/rombong-api/pkg/apperror"
	"github.com/aulansah/rombong-api/pkg/response"
)

// RequestLogger writes one structured log line per request, including the
// request ID so a client-reported error can be traced in the logs.
func RequestLogger(log *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		// The error handler runs after this middleware returns, so the final
		// status is derived from the error when there is one.
		status := c.Response().StatusCode()
		if err != nil {
			status = fiber.StatusInternalServerError
			var appErr *apperror.AppError
			var fiberErr *fiber.Error
			if errors.As(err, &appErr) {
				status = appErr.Status
			} else if errors.As(err, &fiberErr) {
				status = fiberErr.Code
			}
		}

		fields := []zap.Field{
			zap.String("request_id", response.RequestID(c)),
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.Int("status", status),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", c.IP()),
		}
		switch {
		case status >= 500:
			log.Error("http request", fields...)
		case status >= 400:
			log.Warn("http request", fields...)
		default:
			log.Info("http request", fields...)
		}
		return err
	}
}

// RateLimit is a fixed-window limiter backed by Redis, keyed by client IP.
//
// It fails open: when Redis is unavailable the request is allowed, because a
// cache outage must not take the whole API down.
func RateLimit(store cache.Cache, max int, window time.Duration, log *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := "ratelimit:" + c.IP()
		count, err := store.IncrWithTTL(c.UserContext(), key, window)
		if err != nil {
			log.Warn("rate limiter unavailable, allowing request", zap.Error(err))
			return c.Next()
		}

		remaining := max - int(count)
		if remaining < 0 {
			remaining = 0
		}
		c.Set("X-RateLimit-Limit", strconv.Itoa(max))
		c.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

		if int(count) > max {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(window.Seconds())))
			return apperror.TooManyRequests("Too many requests, please try again later")
		}
		return c.Next()
	}
}
