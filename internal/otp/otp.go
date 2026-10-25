// Package otp generates time-based one-time passwords (TOTP, RFC 6238), the
// codes that authenticator apps show, and reads the otpauth:// URIs that
// services hand their settings out in, usually as a QR code.
package otp

import (
	"crypto/hmac"
	"crypto/sha1" // RFC 6238 makes SHA-1 the default; HMAC-SHA1 is still sound
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

// Algorithm is the hash function the codes are computed with.
type Algorithm string

// The algorithms RFC 6238 allows.
const (
	// SHA1 is the default and by far the most common algorithm.
	SHA1 Algorithm = "SHA1"
	// SHA256 is HMAC-SHA-256.
	SHA256 Algorithm = "SHA256"
	// SHA512 is HMAC-SHA-512.
	SHA512 Algorithm = "SHA512"
)

// The settings almost every service uses, assumed when one is not given.
const (
	// DefaultAlgorithm is the algorithm of the codes.
	DefaultAlgorithm = SHA1
	// DefaultDigits is the length of a code.
	DefaultDigits = 6
	// DefaultPeriod is how long a code lives, in seconds.
	DefaultPeriod = 30
)

var (
	// ErrInvalidSecret means the shared secret is empty or is not base32.
	ErrInvalidSecret = errors.New("invalid OTP secret")

	// ErrInvalidParams means the length of the codes, their period or the
	// algorithm is not supported.
	ErrInvalidParams = errors.New("invalid OTP parameters")
)

// Params describe a TOTP generator. The zero Algorithm, Digits and Period
// mean the defaults.
type Params struct {
	// Secret is the shared key in base32, as the services show it; case,
	// spaces and padding do not matter.
	Secret string
	// Issuer is the service the codes are for, such as "GitHub". It does not
	// affect the codes.
	Issuer string
	// Account is the account at the issuer, such as "alice@example.com". It
	// does not affect the codes.
	Account string
	// Algorithm is the hash function of the codes.
	Algorithm Algorithm
	// Digits is the length of a code, from 6 to 8.
	Digits int
	// Period is how long a code lives, in seconds.
	Period int
}

// WithDefaults returns p with the zero settings replaced by the defaults.
func (p Params) WithDefaults() Params {
	if p.Algorithm == "" {
		p.Algorithm = DefaultAlgorithm
	}
	if p.Digits == 0 {
		p.Digits = DefaultDigits
	}
	if p.Period == 0 {
		p.Period = DefaultPeriod
	}

	return p
}

// Validate checks that codes can be generated with p.
func (p Params) Validate() error {
	p = p.WithDefaults()

	if _, err := DecodeSecret(p.Secret); err != nil {
		return err
	}
	if p.Digits < 6 || p.Digits > 8 {
		return fmt.Errorf("%w: codes of %d digits, want 6 to 8", ErrInvalidParams, p.Digits)
	}
	if p.Period <= 0 {
		return fmt.Errorf("%w: period of %d seconds", ErrInvalidParams, p.Period)
	}
	if _, err := p.Algorithm.hash(); err != nil {
		return err
	}

	return nil
}

// Code returns the code that is valid at the moment t.
func Code(p Params, t time.Time) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	p = p.WithDefaults()

	if t.Unix() < 0 {
		return "", fmt.Errorf("%w: time %s is before 1970", ErrInvalidParams, t)
	}

	key, _ := DecodeSecret(p.Secret)
	newHash, _ := p.Algorithm.hash()
	counter := uint64(t.Unix()) / uint64(p.Period)

	return hotp(key, counter, p.Digits, newHash), nil
}

// Remaining returns how long the code that is valid at t stays valid.
func Remaining(p Params, t time.Time) time.Duration {
	p = p.WithDefaults()
	if p.Period <= 0 {
		return 0
	}

	period := time.Duration(p.Period) * time.Second
	elapsed := time.Duration(t.Unix()%int64(p.Period))*time.Second + time.Duration(t.Nanosecond())

	return period - elapsed
}

// FormatCode splits a code in two halves, which are easier to read and type:
// "287082" becomes "287 082".
func FormatCode(code string) string {
	half := len(code) / 2
	return code[:half] + " " + code[half:]
}

// DecodeSecret decodes a base32 secret. It forgives lowercase letters,
// spaces and missing padding, which is how the services often show it.
func DecodeSecret(secret string) ([]byte, error) {
	normalized := strings.ToUpper(strings.Join(strings.Fields(secret), ""))
	normalized = strings.TrimRight(normalized, "=")
	if normalized == "" {
		return nil, fmt.Errorf("%w: the secret is empty", ErrInvalidSecret)
	}

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: not base32: %w", ErrInvalidSecret, err)
	}

	return key, nil
}

// hash returns the constructor of the hash function.
func (a Algorithm) hash() (func() hash.Hash, error) {
	switch Algorithm(strings.ToUpper(string(a))) {
	case SHA1:
		return sha1.New, nil
	case SHA256:
		return sha256.New, nil
	case SHA512:
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("%w: unknown algorithm %q", ErrInvalidParams, string(a))
	}
}

// hotp computes the HOTP code (RFC 4226) of the counter; TOTP is HOTP with
// the number of periods since 1970 as the counter.
func hotp(key []byte, counter uint64, digits int, newHash func() hash.Hash) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)

	mac := hmac.New(newHash, key)
	mac.Write(message[:])
	sum := mac.Sum(nil)

	// Dynamic truncation: the low four bits of the last byte tell where the
	// four bytes of the code start; the top bit is dropped to keep the number
	// positive.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	modulo := uint32(1)
	for range digits {
		modulo *= 10
	}

	return fmt.Sprintf("%0*d", digits, value%modulo)
}
