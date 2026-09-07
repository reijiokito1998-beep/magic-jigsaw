// Package repository contains the Postgres-backed data access layer.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrLocked is returned when an action is blocked because a prerequisite is
// not met (e.g. completing a story page whose predecessor isn't finished).
var ErrLocked = errors.New("locked")

// ErrLevelTooLow is returned when the user's level is below what the content
// requires (e.g. a story gated behind stories.level_required). Distinct from
// ErrLocked because the player cannot resolve it by finishing something else
// nearby — they have to level up first.
var ErrLevelTooLow = errors.New("level too low")

// Store provides access to all persistence operations.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store backed by the given pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Ping verifies the database is reachable. Used by the readiness probe, which
// must fail while the database is down so load balancers stop sending traffic.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Pool exposes the underlying pool for instrumentation (pool-saturation
// metrics). Query code must go through Store methods instead.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
