// Package models describes what the GophKeeper client works with: items as
// the device stores them and syncs them with the server, with the secret
// encrypted; the secrets themselves, decrypted; and the errors all client
// layers share. Besides validating secrets, it holds no business logic.
package models

import (
	"fmt"
	"strings"
	"time"
)

// ItemType is the kind of secret an item holds. The values match the
// ItemType enum of the protocol.
type ItemType int

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

// ItemTypes lists the known types in the order they are offered to the user.
var ItemTypes = []ItemType{ItemTypeCredentials, ItemTypeText, ItemTypeBinary, ItemTypeCard, ItemTypeOTP}

// itemTypeNames are the names the user types and sees.
var itemTypeNames = map[ItemType]string{
	ItemTypeCredentials: "credentials",
	ItemTypeText:        "text",
	ItemTypeBinary:      "binary",
	ItemTypeCard:        "card",
	ItemTypeOTP:         "otp",
}

// String returns the name of the type, such as "card".
func (t ItemType) String() string {
	if name, ok := itemTypeNames[t]; ok {
		return name
	}

	return fmt.Sprintf("ItemType(%d)", int(t))
}

// ParseItemType returns the type with the name String gives it, in any case.
func ParseItemType(name string) (ItemType, error) {
	for _, t := range ItemTypes {
		if strings.EqualFold(itemTypeNames[t], name) {
			return t, nil
		}
	}

	names := make([]string, 0, len(ItemTypes))
	for _, t := range ItemTypes {
		names = append(names, t.String())
	}

	return 0, fmt.Errorf("%w: %q, want one of %s", ErrUnknownType, name, strings.Join(names, ", "))
}

// Item is a secret as the client stores and syncs it: the content is
// encrypted, the sync state is not.
type Item struct {
	// ID is a UUID the client has chosen when the item was created.
	ID string `json:"id"`
	// Type is the kind of the secret.
	Type ItemType `json:"type"`
	// Data is the encrypted Secret.
	Data []byte `json:"data,omitempty"`
	// Version is the server version the local copy is based on; zero for an
	// item the server has not seen yet.
	Version int64 `json:"version,omitempty"`
	// Deleted marks an item deleted on this device whose deletion has not
	// reached the server yet.
	Deleted bool `json:"deleted,omitempty"`
	// Dirty marks an item with local changes the server has not got yet.
	Dirty bool `json:"dirty,omitempty"`
	// UpdatedAt is when the item was last changed, here or on the server.
	UpdatedAt time.Time `json:"updated_at"`
}
