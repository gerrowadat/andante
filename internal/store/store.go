// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Retry the migration lock every second, for up to five minutes. goose's five
// second default would add that long to every start that loses the race.
const (
	lockRetrySeconds  = 1
	lockRetryAttempts = 300
)

// Store is andante's handle on Postgres.
type Store struct {
	pool     *pgxpool.Pool
	sqlDB    *sql.DB
	queries  *db.Queries
	migrator *goose.Provider
}

// Open connects to the database at dsn. It does not migrate; call Migrate.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	migrations, err := fs.Sub(embedded, "migrations")
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(lockRetrySeconds, lockRetryAttempts))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create migration lock: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	migrator, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		_ = sqlDB.Close()
		pool.Close()
		return nil, fmt.Errorf("load migrations: %w", err)
	}

	return &Store{pool: pool, sqlDB: sqlDB, queries: db.New(pool), migrator: migrator}, nil
}

// Migrate applies pending migrations under a Postgres advisory lock, so that
// several servers starting together apply them once.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.migrator.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Ready returns an error unless the database is reachable and has every
// migration applied.
func (s *Store) Ready(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	pending, err := s.migrator.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	if pending {
		return errors.New("migrations are pending")
	}
	return nil
}

// Close releases the store's connections.
func (s *Store) Close() {
	_ = s.migrator.Close()
	_ = s.sqlDB.Close()
	s.pool.Close()
}
