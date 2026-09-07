package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// ErrEmailTaken is returned when registering with an already-used email.
var ErrEmailTaken = errors.New("email already registered")

// CreateUserParams holds the fields for a new email/password account.
type CreateUserParams struct {
	Email        string
	PasswordHash string
	Name         string
	Role         string
}

// CreateUser inserts a new user. Returns ErrEmailTaken on a duplicate email.
func (s *Store) CreateUser(ctx context.Context, p CreateUserParams) (models.User, error) {
	const q = `
INSERT INTO users (email, password_hash, name, role)
VALUES ($1, $2, $3, $4)
RETURNING id, email, name, role, xp, created_at, updated_at`

	log.Printf("[DEBUG] CreateUser: email=%s role=%s", p.Email, p.Role)
	var u models.User
	err := s.pool.QueryRow(ctx, q, p.Email, p.PasswordHash, p.Name, p.Role).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.XP, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			log.Printf("[DEBUG] CreateUser: email already registered: %s", p.Email)
			return models.User{}, ErrEmailTaken
		}
		log.Printf("[DEBUG] CreateUser: query failed: %v", err)
		return models.User{}, fmt.Errorf("create user: %w", err)
	}
	log.Printf("[DEBUG] CreateUser: created user id=%v", u.ID)
	return u, nil
}

// GetUserAuthByEmail returns the user and their stored password hash.
func (s *Store) GetUserAuthByEmail(ctx context.Context, email string) (models.User, string, error) {
	const q = `
SELECT id, email, name, role, xp, created_at, updated_at, password_hash
FROM users WHERE email = $1`

	log.Printf("[DEBUG] GetUserAuthByEmail: email=%s", email)
	var u models.User
	var hash string
	err := s.pool.QueryRow(ctx, q, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.XP, &u.CreatedAt, &u.UpdatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetUserAuthByEmail: not found email=%s", email)
		return models.User{}, "", ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetUserAuthByEmail: query failed: %v", err)
		return models.User{}, "", fmt.Errorf("get user by email: %w", err)
	}
	// Note: hash is the bcrypt/argon password hash and is intentionally never logged.
	log.Printf("[DEBUG] GetUserAuthByEmail: found userID=%v", u.ID)
	return u, hash, nil
}

// UpdateUserRole sets a user's role (used to keep admins in sync with config).
func (s *Store) UpdateUserRole(ctx context.Context, id uuid.UUID, role string) error {
	const q = `UPDATE users SET role = $2, updated_at = now() WHERE id = $1`
	log.Printf("[DEBUG] UpdateUserRole: id=%v role=%s", id, role)
	if _, err := s.pool.Exec(ctx, q, id, role); err != nil {
		log.Printf("[DEBUG] UpdateUserRole: query failed: %v", err)
		return fmt.Errorf("update user role: %w", err)
	}
	return nil
}

// GetUser fetches a user by id.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (models.User, error) {
	const q = `
SELECT id, email, name, role, xp, created_at, updated_at
FROM users WHERE id = $1`

	log.Printf("[DEBUG] GetUser: id=%v", id)
	var u models.User
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.XP, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetUser: not found id=%v", id)
		return models.User{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetUser: query failed: %v", err)
		return models.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// GetXP returns a user's current total experience points.
func (s *Store) GetXP(ctx context.Context, id uuid.UUID) (int, error) {
	var xp int
	if err := s.pool.QueryRow(ctx, `SELECT xp FROM users WHERE id = $1`, id).Scan(&xp); err != nil {
		log.Printf("[DEBUG] GetXP: query failed: %v", err)
		return 0, fmt.Errorf("get xp: %w", err)
	}
	return xp, nil
}

// AddXP increments a user's experience points and returns the new total.
func (s *Store) AddXP(ctx context.Context, id uuid.UUID, amount int) (int, error) {
	const q = `UPDATE users SET xp = xp + $2, updated_at = now() WHERE id = $1 RETURNING xp`
	log.Printf("[DEBUG] AddXP: id=%v amount=%d", id, amount)
	var total int
	if err := s.pool.QueryRow(ctx, q, id, amount).Scan(&total); err != nil {
		log.Printf("[DEBUG] AddXP: query failed: %v", err)
		return 0, fmt.Errorf("add xp: %w", err)
	}
	log.Printf("[DEBUG] AddXP: id=%v new total xp=%d", id, total)
	return total, nil
}
