// Package models describes the entities of the GophKeeper server, users and
// their items, and the errors all server layers share. It holds no business
// logic.
//
// The server never sees the secrets themselves: an item carries data the
// client has encrypted with a key that does not leave the user's devices.
package models

import "time"

// MaxItemSize is the largest encrypted item data in bytes the server accepts.
// It leaves room for an 8 MiB file once the client has encoded and
// encrypted it.
const MaxItemSize = 12 << 20 // 12 MiB

// User is a registered owner of items.
type User struct {
	// ID is the surrogate key the items refer to the user by.
	ID int64
	// Login is unique across all users.
	Login string
	// PasswordHash is the bcrypt hash of the password; the password itself
	// is never stored.
	PasswordHash string
}

// ItemType is the kind of secret an item holds. The values match the
// ItemType enum of the protocol.
type ItemType int16

// The known item types.
const (
	// ItemTypeCredentials is a login and password pair.
	ItemTypeCredentials ItemType = iota + 1
	// ItemTypeText is arbitrary text.
	ItemTypeText
	// ItemTypeBinary is arbitrary binary data, such as a file.
	ItemTypeBinary
	// ItemTypeCard is bank card details.
	ItemTypeCard
	// ItemTypeOTP is a generator of one-time passwords.
	ItemTypeOTP
)

// Valid reports whether t is one of the known item types.
func (t ItemType) Valid() bool {
	return t >= ItemTypeCredentials && t <= ItemTypeOTP
}

// Item is a secret of a user as the server keeps it.
type Item struct {
	// ID is a UUID in its canonical lowercase form. The client chooses it,
	// so an item created offline keeps its ID after the sync.
	ID string
	// Type is the kind of the secret.
	Type ItemType
	// Data is the secret encrypted by the client. It is empty for a deleted
	// item.
	Data []byte
	// Version grows with every change of any item of the user. The items of
	// one user never share a version.
	Version int64
	// Deleted marks a tombstone: the item is kept without data so that the
	// other clients of the user learn about the deletion.
	Deleted bool
	// UpdatedAt is when the latest change was stored.
	UpdatedAt time.Time
}
