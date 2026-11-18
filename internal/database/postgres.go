// Package database owns the PostgreSQL connection, migrations and the
// transaction helper shared by all repositories.
package database

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/aulansah/go-ecommerce-api/internal/config"
)

// Connect opens the GORM connection and configures the connection pool.
func Connect(cfg config.DBConfig, log *zap.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		// GORM's own logger is silenced; errors surface through the
		// application error handler with a request ID attached.
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		// TranslateError maps driver errors to gorm.ErrDuplicatedKey etc.,
		// so repositories do not depend on PostgreSQL error codes.
		TranslateError: true,
		NowFunc:        func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Info("database connected", zap.String("host", cfg.Host), zap.String("name", cfg.Name))
	return db, nil
}

type txKey struct{}

// TxManager lets the service layer define a transaction boundary without
// importing GORM. The transaction travels inside the context, and every
// repository picks it up through FromContext.
type TxManager struct {
	db *gorm.DB
}

func NewTxManager(db *gorm.DB) *TxManager { return &TxManager{db: db} }

// WithinTx runs fn inside one database transaction. Returning an error (or
// panicking) rolls everything back; returning nil commits.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// FromContext returns the transaction stored in ctx, or fallback when the
// call is not part of a transaction.
func FromContext(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx
	}
	return fallback.WithContext(ctx)
}
