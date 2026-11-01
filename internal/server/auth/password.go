package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MaxPasswordLength is the longest password in bytes that can be hashed:
// bcrypt ignores everything past it.
const MaxPasswordLength = 72

// HashPassword returns the bcrypt hash of the password, salted and ready to be
// stored.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	return string(hash), nil
}

// CheckPassword reports whether the password matches the bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
