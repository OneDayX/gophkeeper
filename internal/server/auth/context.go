package auth

import (
	"context"
	"strings"
)

// MetadataKey is the gRPC metadata key that carries the token. grpc-gateway
// passes the Authorization header of a REST request under the same key.
const MetadataKey = "authorization"

// bearerScheme is the scheme that carries a token.
const bearerScheme = "Bearer"

// userIDKey is the context key for the authenticated user ID. An unexported
// type keeps other packages from colliding with it.
type userIDKey struct{}

// WithUserID returns a copy of ctx that carries the ID of the authenticated
// user.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

// UserID returns the ID of the authenticated user that ctx carries. The flag
// is false if the call has not been authenticated.
func UserID(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDKey{}).(int64)
	return userID, ok
}

// BearerHeader returns the authorization value that carries the token.
func BearerHeader(token string) string {
	return bearerScheme + " " + token
}

// TokenFromHeader extracts the token from an authorization value. The scheme
// is optional and case-insensitive, so both "Bearer <token>" and a bare token
// are accepted. Any other scheme gives an empty token.
func TokenFromHeader(header string) string {
	scheme, token, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found {
		// No scheme, just the token.
		return scheme
	}

	if !strings.EqualFold(scheme, bearerScheme) {
		// Another scheme, such as Basic, carries no token of ours.
		return ""
	}

	return strings.TrimSpace(token)
}
