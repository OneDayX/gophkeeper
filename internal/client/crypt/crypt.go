// Package crypt holds the cryptography of the client.
//
// The master password never leaves the device. The client derives two keys
// from it: one encrypts the secrets, the other proves the password to the
// server and is sent in its place. The server keeps only a bcrypt hash of the
// second key, so even a compromised server sees neither the password nor the
// secrets, and cannot derive the encryption key from what it gets.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// KeySize is the size of the keys in bytes: AES-256 takes 32.
const KeySize = 32

// The Argon2id parameters the argon2 package recommends: one pass over
// 64 MiB with four threads, about a tenth of a second on a laptop. They must
// never change, or the users would lose access to their secrets.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
)

// saltPrefix keeps the salts of GophKeeper apart from those of other
// programs that might derive keys from the same password.
const saltPrefix = "gophkeeper:"

// ErrDecrypt means the data cannot be decrypted: the key is wrong, or the
// data or what it is bound to has been tampered with.
var ErrDecrypt = errors.New("cannot decrypt: wrong key or corrupted data")

// Keys are what the client derives from the master password.
type Keys struct {
	// Auth goes to the server in place of the password, hex-encoded.
	Auth string
	// Encryption encrypts the secrets. It never leaves the device.
	Encryption []byte
}

// DeriveKeys derives the keys from the login and the master password. The
// same pair gives the same keys on every device, so all devices of a user
// can decrypt the secrets.
func DeriveKeys(login, password string) Keys {
	// The salt comes from the login: every device can compute it without
	// asking the server, and two users with one password get different keys.
	salt := sha256.Sum256([]byte(saltPrefix + login))
	master := argon2.IDKey([]byte(password), salt[:], argonTime, argonMemory, argonThreads, KeySize)

	return Keys{
		Auth:       hex.EncodeToString(subkey(master, "auth")),
		Encryption: subkey(master, "encryption"),
	}
}

// subkey derives a key for one purpose from the master key. HMAC makes the
// subkeys independent: one tells nothing about another or the master key.
func subkey(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte(purpose))

	return mac.Sum(nil)
}

// Encrypt encrypts the plaintext with AES-256-GCM under the key. The
// additional data is not encrypted but bound to the result: Decrypt fails if
// it is given other additional data. The result is a random nonce followed
// by the ciphertext and its authentication tag.
func Encrypt(key, plaintext, additionalData []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("make nonce: %w", err)
	}

	return aead.Seal(nonce, nonce, plaintext, additionalData), nil
}

// Decrypt reverses Encrypt. It returns ErrDecrypt if the key is wrong or the
// data or the additional data are not what Encrypt was given.
func Decrypt(key, data, additionalData []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	if len(data) < aead.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ciphertext := data[:aead.NonceSize()], data[aead.NonceSize():]

	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, ErrDecrypt
	}

	return plaintext, nil
}

// newAEAD returns AES-GCM with the key.
func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	return cipher.NewGCM(block)
}
