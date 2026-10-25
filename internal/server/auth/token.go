// Package auth issues and checks the tokens users authenticate with, hashes
// their passwords, and carries the authenticated user through a request
// context.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken means the token is malformed, expired, or signed with
// another key.
var ErrInvalidToken = errors.New("invalid token")

// claims is the payload of a token.
type claims struct {
	jwt.RegisteredClaims
	UserID int64 `json:"uid"`
}

// Tokens issues and parses JWTs signed with HMAC-SHA256 that carry a user ID.
// It keeps no state besides the key, so any number of goroutines may use it.
type Tokens struct {
	key []byte
	ttl time.Duration
}

// NewTokens returns Tokens that sign with key and issue tokens valid for ttl.
func NewTokens(key string, ttl time.Duration) *Tokens {
	return &Tokens{key: []byte(key), ttl: ttl}
}

// Issue returns a signed token that authenticates the user with this ID.
func (t *Tokens) Issue(userID int64) (string, error) {
	now := time.Now()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
		},
		UserID: userID,
	})

	signed, err := token.SignedString(t.key)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return signed, nil
}

// Parse checks the token and returns the user ID it carries. Every failure
// wraps ErrInvalidToken.
func (t *Tokens) Parse(token string) (int64, error) {
	var c claims

	// The method is pinned, so a token cannot choose "none" or an asymmetric
	// algorithm and have the key misused as a public key.
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return t.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	if c.UserID <= 0 {
		return 0, fmt.Errorf("%w: no user ID", ErrInvalidToken)
	}

	return c.UserID, nil
}
