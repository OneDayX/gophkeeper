package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserRepository stores users in PostgreSQL.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository returns a UserRepository that runs its queries on pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create stores a new user and returns its ID. It returns
// models.ErrLoginTaken if another user has the login.
func (r *UserRepository) Create(ctx context.Context, login, passwordHash string) (int64, error) {
	var id int64

	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (login, password_hash) VALUES ($1, $2) RETURNING id`,
		login, passwordHash,
	).Scan(&id)

	// The unique key settles a race between two registrations of one login,
	// which a check before the insert would not.
	if isUniqueViolation(err) {
		return 0, fmt.Errorf("%w: %q", models.ErrLoginTaken, login)
	}
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}

	return id, nil
}

// GetByLogin returns the user with this login, or models.ErrUserNotFound.
func (r *UserRepository) GetByLogin(ctx context.Context, login string) (models.User, error) {
	var user models.User

	err := r.pool.QueryRow(ctx,
		`SELECT id, login, password_hash FROM users WHERE login = $1`,
		login,
	).Scan(&user.ID, &user.Login, &user.PasswordHash)

	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, fmt.Errorf("%w: %q", models.ErrUserNotFound, login)
	}
	if err != nil {
		return models.User{}, fmt.Errorf("get user: %w", err)
	}

	return user, nil
}
