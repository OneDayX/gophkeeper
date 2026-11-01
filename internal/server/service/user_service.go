// Package service holds the business logic of the GophKeeper server: signing
// users up and in and keeping their encrypted items. It knows nothing about
// gRPC or SQL and reaches the data through small interfaces the repositories
// satisfy.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"github.com/OneDayX/gophkeeper/internal/server/models"
)

// maxLoginLength is the longest login in bytes, a bound for what a client can
// make the database store.
const maxLoginLength = 256

type userStore interface {
	Create(ctx context.Context, login, passwordHash string) (int64, error)
	GetByLogin(ctx context.Context, login string) (models.User, error)
}

type tokenIssuer interface {
	Issue(userID int64) (string, error)
}

// UserService registers and authenticates users.
type UserService struct {
	users  userStore
	tokens tokenIssuer
}

// NewUserService returns a UserService that keeps users in the store and
// authenticates them with tokens from the issuer.
func NewUserService(users userStore, tokens tokenIssuer) *UserService {
	return &UserService{users: users, tokens: tokens}
}

// Register creates a user and returns a token that authenticates them. It
// returns models.ErrMalformedCredentials for an empty or too long login or
// password, and models.ErrLoginTaken if the login is in use.
func (s *UserService) Register(ctx context.Context, login, password string) (string, error) {
	if err := validateCredentials(login, password); err != nil {
		return "", err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}

	userID, err := s.users.Create(ctx, login, hash)
	if err != nil {
		return "", err
	}

	return s.tokens.Issue(userID)
}

// Login checks the password of the user and returns a token that
// authenticates them. It returns models.ErrInvalidCredentials for an unknown
// login and for a wrong password alike.
func (s *UserService) Login(ctx context.Context, login, password string) (string, error) {
	if err := validateCredentials(login, password); err != nil {
		return "", err
	}

	user, err := s.users.GetByLogin(ctx, login)
	if errors.Is(err, models.ErrUserNotFound) {
		return "", fmt.Errorf("%w: %w", models.ErrInvalidCredentials, err)
	}
	if err != nil {
		return "", err
	}

	if !auth.CheckPassword(user.PasswordHash, password) {
		return "", fmt.Errorf("%w: wrong password for %q", models.ErrInvalidCredentials, login)
	}

	return s.tokens.Issue(user.ID)
}

// validateCredentials checks the pair before it reaches bcrypt or the
// database.
func validateCredentials(login, password string) error {
	switch {
	case login == "" || password == "":
		return fmt.Errorf("%w: login and password must not be empty", models.ErrMalformedCredentials)
	case len(login) > maxLoginLength:
		return fmt.Errorf("%w: login is longer than %d bytes", models.ErrMalformedCredentials, maxLoginLength)
	case len(password) > auth.MaxPasswordLength:
		return fmt.Errorf("%w: password is longer than %d bytes", models.ErrMalformedCredentials, auth.MaxPasswordLength)
	default:
		return nil
	}
}
