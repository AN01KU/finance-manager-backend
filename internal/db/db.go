package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, dbURL string, maxConns, minConns int32) (*DB, error) {
	// Configure connection pool
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set pool settings for better performance
	config.MaxConns = maxConns                 // Configurable via DB_MAX_CONNS (default 25)
	config.MinConns = minConns                 // Configurable via DB_MIN_CONNS (default 5)
	config.MaxConnLifetime = 1 * time.Hour     // Maximum connection lifetime
	config.MaxConnIdleTime = 30 * time.Minute  // Maximum idle time
	config.HealthCheckPeriod = 1 * time.Minute // Health check frequency

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping db: %w", err)
	}

	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}

// RunMigrations runs database migrations. If migrationsPath is non-empty,
// migrations are loaded from that directory path (useful for development /
// custom overrides). Otherwise the embedded migrations are used so the binary
// works regardless of the working directory.
func RunMigrations(ctx context.Context, dbURL, migrationsPath string) error {
	slog.Debug("creating migration pool connection")
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create pool for migration: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres driver: %w", err)
	}

	var m *migrate.Migrate
	if migrationsPath != "" {
		m, err = migrate.NewWithDatabaseInstance(
			fmt.Sprintf("file://%s", migrationsPath),
			"postgres", driver)
	} else {
		src, srcErr := iofs.New(MigrationsFS, "migrations")
		if srcErr != nil {
			return fmt.Errorf("failed to create iofs source: %w", srcErr)
		}
		m, err = migrate.NewWithInstance("iofs", src, "postgres", driver)
	}
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	slog.Debug("executing migrations")
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	slog.Info("migrations executed")

	// Note: Not closing the migration pool here to avoid deadlocks with the migrate library
	// The pool will be garbage collected when the function returns

	return nil
}
