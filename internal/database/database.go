package database

import (
	"context"
	"embed"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Connect opens a pgx connection pool and verifies connectivity. maxConns bounds
// the pool size; keep it at or below the database's max_connections divided by
// the number of running instances (front the DB with PgBouncer to scale past
// that). A value <= 0 falls back to a safe default.
func Connect(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns <= 0 {
		maxConns = 20
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	log.Printf("database: connecting to host=%s db=%s (max_conns=%d, min_conns=%d)",
		cfg.ConnConfig.Host, cfg.ConnConfig.Database, cfg.MaxConns, cfg.MinConns)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Printf("database: create pool failed for host=%s db=%s: %v", cfg.ConnConfig.Host, cfg.ConnConfig.Database, err)
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		log.Printf("database: ping failed for host=%s db=%s: %v", cfg.ConnConfig.Host, cfg.ConnConfig.Database, err)
		return nil, fmt.Errorf("ping database: %w", err)
	}
	log.Printf("database: connected successfully to host=%s db=%s (max_conns=%d, min_conns=%d, max_conn_lifetime=%s, max_conn_idle_time=%s)",
		cfg.ConnConfig.Host, cfg.ConnConfig.Database, cfg.MaxConns, cfg.MinConns, cfg.MaxConnLifetime, cfg.MaxConnIdleTime)
	return pool, nil
}

// Migrate applies every embedded .sql migration in lexical order. Each file is
// executed inside its own transaction. Statements are expected to be
// idempotent (IF NOT EXISTS), so re-running is safe.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	log.Printf("database: applying %d migration(s)", len(names))
	for _, name := range names {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			log.Printf("database: migration %s failed: %v", name, err)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			log.Printf("database: commit migration %s failed: %v", name, err)
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
		log.Printf("database: migration %s applied", name)
	}
	log.Printf("database: all migrations applied successfully")
	return nil
}
