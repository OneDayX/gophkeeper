// Package database opens the PostgreSQL connection pool the repositories work
// through and keeps the schema up to date.
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is a connection pool to a database with an up-to-date schema.
type DB struct {
	pool *pgxpool.Pool
}

// New applies pending migrations and opens a connection pool. It pings the
// database, so a wrong DSN fails here rather than on the first request.
func New(ctx context.Context, dsn string) (*DB, error) {
	if err := migrateSchema(dsn); err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Pool returns the connection pool for the repositories to run queries on.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Close closes all connections of the pool. It waits for the ones in use to
// be released.
func (db *DB) Close() {
	db.pool.Close()
}
