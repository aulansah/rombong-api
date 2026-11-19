// Package config loads application configuration from environment variables
// (and an optional .env file for local development) using Viper.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App       AppConfig
	DB        DBConfig
	Redis     RedisConfig
	JWT       JWTConfig
	CORS      CORSConfig
	RateLimit RateLimitConfig
	Cache     CacheConfig
	Payment   PaymentConfig
	Admin     AdminConfig
}

type AppConfig struct {
	Name        string
	Env         string
	Port        string
	LogLevel    string
	AutoMigrate bool
	BcryptCost  int
}

type DBConfig struct {
	Host         string
	Port         string
	User         string
	Password     string
	Name         string
	SSLMode      string
	MaxOpenConns int
	MaxIdleConns int
}

// DSN returns the keyword/value connection string understood by pgx.
func (d DBConfig) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode)
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type JWTConfig struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type CORSConfig struct {
	AllowOrigins string
}

type RateLimitConfig struct {
	Enabled bool
	Max     int
	Window  time.Duration
}

type CacheConfig struct {
	ProductTTL     time.Duration
	ProductListTTL time.Duration
}

type PaymentConfig struct {
	Expiry time.Duration
}

// AdminConfig seeds the first admin account at startup. Public registration
// always creates customers, so this is the only way an admin comes to exist.
type AdminConfig struct {
	Name     string
	Email    string
	Password string
}

// IsProduction reports whether the app runs with production settings.
func (c *Config) IsProduction() bool { return c.App.Env == "production" }

// Load reads configuration. Real environment variables always win over the
// .env file, which keeps the same binary usable locally and in containers.
func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	v.AutomaticEnv()
	setDefaults(v)

	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read .env: %w", err)
	}

	cfg := &Config{
		App: AppConfig{
			Name:        v.GetString("APP_NAME"),
			Env:         v.GetString("APP_ENV"),
			Port:        v.GetString("APP_PORT"),
			LogLevel:    v.GetString("LOG_LEVEL"),
			AutoMigrate: v.GetBool("AUTO_MIGRATE"),
			BcryptCost:  v.GetInt("BCRYPT_COST"),
		},
		DB: DBConfig{
			Host:         v.GetString("DB_HOST"),
			Port:         v.GetString("DB_PORT"),
			User:         v.GetString("DB_USER"),
			Password:     v.GetString("DB_PASSWORD"),
			Name:         v.GetString("DB_NAME"),
			SSLMode:      v.GetString("DB_SSLMODE"),
			MaxOpenConns: v.GetInt("DB_MAX_OPEN_CONNS"),
			MaxIdleConns: v.GetInt("DB_MAX_IDLE_CONNS"),
		},
		Redis: RedisConfig{
			Addr:     v.GetString("REDIS_ADDR"),
			Password: v.GetString("REDIS_PASSWORD"),
			DB:       v.GetInt("REDIS_DB"),
		},
		JWT: JWTConfig{
			Secret:     v.GetString("JWT_SECRET"),
			AccessTTL:  v.GetDuration("JWT_ACCESS_TTL"),
			RefreshTTL: v.GetDuration("JWT_REFRESH_TTL"),
		},
		CORS: CORSConfig{AllowOrigins: v.GetString("CORS_ALLOW_ORIGINS")},
		RateLimit: RateLimitConfig{
			Enabled: v.GetBool("RATE_LIMIT_ENABLED"),
			Max:     v.GetInt("RATE_LIMIT_MAX"),
			Window:  v.GetDuration("RATE_LIMIT_WINDOW"),
		},
		Cache: CacheConfig{
			ProductTTL:     v.GetDuration("CACHE_PRODUCT_TTL"),
			ProductListTTL: v.GetDuration("CACHE_PRODUCT_LIST_TTL"),
		},
		Payment: PaymentConfig{Expiry: v.GetDuration("PAYMENT_EXPIRY")},
		Admin: AdminConfig{
			Name:     v.GetString("ADMIN_NAME"),
			Email:    strings.ToLower(strings.TrimSpace(v.GetString("ADMIN_EMAIL"))),
			Password: v.GetString("ADMIN_PASSWORD"),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("APP_NAME", "rombong-api")
	v.SetDefault("APP_ENV", "development")
	v.SetDefault("APP_PORT", "8080")
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("AUTO_MIGRATE", true)
	v.SetDefault("BCRYPT_COST", 12)

	v.SetDefault("DB_HOST", "localhost")
	v.SetDefault("DB_PORT", "5432")
	v.SetDefault("DB_USER", "postgres")
	v.SetDefault("DB_PASSWORD", "postgres")
	v.SetDefault("DB_NAME", "ecommerce")
	v.SetDefault("DB_SSLMODE", "disable")
	v.SetDefault("DB_MAX_OPEN_CONNS", 25)
	v.SetDefault("DB_MAX_IDLE_CONNS", 5)

	v.SetDefault("REDIS_ADDR", "localhost:6379")
	v.SetDefault("REDIS_PASSWORD", "")
	v.SetDefault("REDIS_DB", 0)

	v.SetDefault("JWT_SECRET", "")
	v.SetDefault("JWT_ACCESS_TTL", "15m")
	v.SetDefault("JWT_REFRESH_TTL", "168h")

	v.SetDefault("CORS_ALLOW_ORIGINS", "http://localhost:3000")

	v.SetDefault("RATE_LIMIT_ENABLED", true)
	v.SetDefault("RATE_LIMIT_MAX", 100)
	v.SetDefault("RATE_LIMIT_WINDOW", "1m")

	v.SetDefault("CACHE_PRODUCT_TTL", "10m")
	v.SetDefault("CACHE_PRODUCT_LIST_TTL", "2m")

	v.SetDefault("PAYMENT_EXPIRY", "30m")

	v.SetDefault("ADMIN_NAME", "Administrator")
	v.SetDefault("ADMIN_EMAIL", "")
	v.SetDefault("ADMIN_PASSWORD", "")
}

// validate fails fast on configuration that would make the app insecure or
// unable to start, instead of discovering it on the first request.
func (c *Config) validate() error {
	if len(c.JWT.Secret) < 32 {
		return errors.New("JWT_SECRET must be set and at least 32 characters long")
	}
	if c.JWT.AccessTTL <= 0 || c.JWT.RefreshTTL <= 0 {
		return errors.New("JWT_ACCESS_TTL and JWT_REFRESH_TTL must be positive durations")
	}
	if c.App.BcryptCost < 4 || c.App.BcryptCost > 31 {
		return errors.New("BCRYPT_COST must be between 4 and 31")
	}
	if c.IsProduction() && strings.TrimSpace(c.CORS.AllowOrigins) == "*" {
		return errors.New("CORS_ALLOW_ORIGINS must not be '*' in production")
	}
	return nil
}
