package models

import "errors"

// Sentinel errors shared by the repositories, the services and the gRPC
// handlers. The handlers turn them into gRPC status codes with errors.Is, so
// the layers below wrap them instead of replacing.
var (
	// ErrMalformedCredentials means the login or the password is empty or too
	// long to be stored.
	ErrMalformedCredentials = errors.New("malformed credentials")

	// ErrLoginTaken means another user has already registered with this login.
	ErrLoginTaken = errors.New("login is already taken")

	// ErrInvalidCredentials means the login is unknown or the password does
	// not match it. The two cases are not told apart on purpose.
	ErrInvalidCredentials = errors.New("invalid login or password")

	// ErrUserNotFound means there is no user with this login or ID.
	ErrUserNotFound = errors.New("user not found")

	// ErrInvalidItem means the request names an item badly or carries bad
	// data: the ID is not a UUID, the type is unknown, the data is empty or
	// too large.
	ErrInvalidItem = errors.New("invalid item")

	// ErrItemExists means the user already has an item with this ID.
	ErrItemExists = errors.New("item already exists")

	// ErrItemNotFound means the user has no item with this ID, or it has been
	// deleted.
	ErrItemNotFound = errors.New("item not found")

	// ErrVersionConflict means the item has changed since the version the
	// client knows: another client of the user has got there first.
	ErrVersionConflict = errors.New("item version conflict")
)
