package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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
	config.MaxConns = maxConns                  // Configurable via DB_MAX_CONNS (default 25)
	config.MinConns = minConns                  // Configurable via DB_MIN_CONNS (default 5)
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

func RunMigrations(ctx context.Context, dbURL, migrationsPath string) error {
	log.Println("  → Creating migration pool connection...")
	// Create a sql.DB from pgx pool for migrate
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create pool for migration: %w", err)
	}
	log.Println("  ✓ Migration pool connected")

	log.Println("  → Pinging database...")
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	log.Println("  ✓ Database ping successful")

	sqlDB := stdlib.OpenDBFromPool(pool)

	log.Println("  → Creating Postgres driver...")
	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres driver: %w", err)
	}
	log.Println("  ✓ Postgres driver created")

	log.Println("  → Initializing migration instance...")
	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", migrationsPath),
		"postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	log.Println("  ✓ Migration instance initialized")

	log.Println("  → Executing migrations...")
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	log.Println("  ✓ All migrations executed successfully")

	// Note: Not closing the migration pool here to avoid deadlocks with the migrate library
	// The pool will be garbage collected when the function returns

	return nil
}
