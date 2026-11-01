package crypt

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveKeys(t *testing.T) {
	keys := DeriveKeys("alice", "correct horse battery staple")

	assert.Len(t, keys.Encryption, KeySize)
	assert.Len(t, keys.Auth, 2*KeySize, "hex of 32 bytes")

	// Another device gets the same keys from the same login and password.
	assert.Equal(t, keys, DeriveKeys("alice", "correct horse battery staple"))

	// What the server gets is not the encryption key.
	assert.NotEqual(t, hex.EncodeToString(keys.Encryption), keys.Auth)

	other := DeriveKeys("alice", "another password")
	assert.NotEqual(t, keys.Encryption, other.Encryption)
	assert.NotEqual(t, keys.Auth, other.Auth)

	// The login salts the keys: one password, different users.
	bob := DeriveKeys("bob", "correct horse battery staple")
	assert.NotEqual(t, keys.Encryption, bob.Encryption)
	assert.NotEqual(t, keys.Auth, bob.Auth)
}

func TestEncryptDecrypt(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeySize)
	plaintext := []byte("the secret")
	ad := []byte("item-1")

	sealed, err := Encrypt(key, plaintext, ad)
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), "the secret")

	opened, err := Decrypt(key, sealed, ad)
	require.NoError(t, err)
	assert.Equal(t, plaintext, opened)

	// The nonce is random, so the same plaintext encrypts differently.
	again, err := Encrypt(key, plaintext, ad)
	require.NoError(t, err)
	assert.NotEqual(t, sealed, again)
}

func TestDecrypt_Tampering(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeySize)
	sealed, err := Encrypt(key, []byte("the secret"), []byte("item-1"))
	require.NoError(t, err)

	_, err = Decrypt(bytes.Repeat([]byte{8}, KeySize), sealed, []byte("item-1"))
	assert.ErrorIs(t, err, ErrDecrypt, "wrong key")

	_, err = Decrypt(key, sealed, []byte("item-2"))
	assert.ErrorIs(t, err, ErrDecrypt, "the data moved to another item")

	flipped := bytes.Clone(sealed)
	flipped[len(flipped)-1] ^= 1
	_, err = Decrypt(key, flipped, []byte("item-1"))
	assert.ErrorIs(t, err, ErrDecrypt, "a flipped bit")

	_, err = Decrypt(key, sealed[:5], []byte("item-1"))
	assert.ErrorIs(t, err, ErrDecrypt, "shorter than a nonce")
}

func TestBadKey(t *testing.T) {
	_, err := Encrypt([]byte("short"), []byte("x"), nil)
	assert.Error(t, err)

	_, err = Decrypt([]byte("short"), []byte("x"), nil)
	assert.Error(t, err)
}
