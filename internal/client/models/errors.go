package models

import "errors"

// Sentinel errors shared by the client layers. The layers below wrap them, so
// the commands and the TUI find out what happened with errors.Is.
var (
	// ErrNotLoggedIn means there is no session on this device.
	ErrNotLoggedIn = errors.New("not logged in, run `gophkeeper login` or `gophkeeper register` first")

	// ErrInvalidSecret means a secret fails validation: no name, a bad card
	// number, a file too large and so on.
	ErrInvalidSecret = errors.New("invalid secret")

	// ErrUnknownType means an item type is not one of ItemTypes.
	ErrUnknownType = errors.New("unknown item type")

	// ErrNotFound means there is no item with this ID or name.
	ErrNotFound = errors.New("item not found")

	// ErrAmbiguous means more than one item matches a name or an ID prefix.
	ErrAmbiguous = errors.New("more than one item matches")

	// ErrConflict means the item has changed on the server since this device
	// last saw it: another device has got there first.
	ErrConflict = errors.New("item has been changed on another device")

	// ErrLoginTaken means another user has already registered with the login.
	ErrLoginTaken = errors.New("login is already taken")

	// ErrInvalidCredentials means the login is unknown or the password is
	// wrong.
	ErrInvalidCredentials = errors.New("invalid login or password")

	// ErrUnauthenticated means the server has not accepted the session token,
	// usually because it has expired.
	ErrUnauthenticated = errors.New("session has expired, run `gophkeeper login` again")

	// ErrUnavailable means the server cannot be reached. The local changes
	// are kept and will be synced later.
	ErrUnavailable = errors.New("server is unavailable")

	// ErrRejected means the server has refused a request as malformed.
	ErrRejected = errors.New("server has rejected the request")
)
