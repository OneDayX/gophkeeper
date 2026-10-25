package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokens(t *testing.T) {
	tokens := NewTokens("secret", time.Hour)

	token, err := tokens.Issue(42)
	require.NoError(t, err)

	userID, err := tokens.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, int64(42), userID)

	_, err = tokens.Parse("not a token")
	assert.ErrorIs(t, err, ErrInvalidToken)

	foreign, err := NewTokens("other", time.Hour).Issue(42)
	require.NoError(t, err)
	_, err = tokens.Parse(foreign)
	assert.ErrorIs(t, err, ErrInvalidToken)

	expired, err := NewTokens("secret", -time.Minute).Issue(42)
	require.NoError(t, err)
	_, err = tokens.Parse(expired)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestTokens_RejectsForeignClaims(t *testing.T) {
	tokens := NewTokens("secret", time.Hour)

	// A valid signature over claims without a user ID.
	noUser, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte("secret"))
	require.NoError(t, err)
	_, err = tokens.Parse(noUser)
	assert.ErrorIs(t, err, ErrInvalidToken)

	// The "none" algorithm must not pass, whatever the claims.
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		UserID:           1,
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, err = tokens.Parse(unsigned)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestPassword(t *testing.T) {
	hash, err := HashPassword("secret")
	require.NoError(t, err)

	assert.True(t, CheckPassword(hash, "secret"))
	assert.False(t, CheckPassword(hash, "wrong"))
}

func TestUserID(t *testing.T) {
	_, ok := UserID(context.Background())
	assert.False(t, ok)

	userID, ok := UserID(WithUserID(context.Background(), 7))
	assert.True(t, ok)
	assert.Equal(t, int64(7), userID)
}

func TestTokenFromHeader(t *testing.T) {
	assert.Equal(t, "abc", TokenFromHeader(BearerHeader("abc")))
	assert.Equal(t, "abc", TokenFromHeader("bearer  abc "))
	assert.Equal(t, "abc", TokenFromHeader("abc"))
	assert.Equal(t, "", TokenFromHeader("Basic abc"))
	assert.Equal(t, "", TokenFromHeader(""))
}
