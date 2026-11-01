package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/OneDayX/gophkeeper/internal/luhn"
	"github.com/OneDayX/gophkeeper/internal/otp"
)

const (
	// MaxNameLength is the longest name of a secret in bytes.
	MaxNameLength = 256

	// MaxFileSize is the largest binary secret in bytes. Encoded and
	// encrypted, it still fits the item size limit of the server.
	MaxFileSize = 8 << 20 // 8 MiB
)

var (
	// expiryPattern is MM/YY.
	expiryPattern = regexp.MustCompile(`^(0[1-9]|1[0-2])/[0-9]{2}$`)
	// cvvPattern is three or four digits.
	cvvPattern = regexp.MustCompile(`^[0-9]{3,4}$`)
)

// Secret is the decrypted content of an item: what the user stores.
type Secret struct {
	// Name tells the secrets apart, such as "GitHub" or "Salary card".
	Name string
	// Metadata is free text kept with the secret: the site, person or bank
	// it belongs to, notes, a list of one-time activation codes and so on.
	Metadata string
	// Payload is the part that depends on the type of the secret.
	Payload Payload
}

// Type returns the type of the payload, or zero if there is none.
func (s Secret) Type() ItemType {
	if s.Payload == nil {
		return 0
	}

	return s.Payload.Type()
}

// Validate checks the name and the payload. Every failure wraps
// ErrInvalidSecret.
func (s Secret) Validate() error {
	switch {
	case strings.TrimSpace(s.Name) == "":
		return fmt.Errorf("%w: the name is empty", ErrInvalidSecret)
	case len(s.Name) > MaxNameLength:
		return fmt.Errorf("%w: the name is longer than %d bytes", ErrInvalidSecret, MaxNameLength)
	case s.Payload == nil:
		return fmt.Errorf("%w: no content", ErrInvalidSecret)
	default:
		return s.Payload.Validate()
	}
}

// Payload is the type-specific part of a secret: Credentials, Text, Binary,
// Card or OTP.
type Payload interface {
	// Type returns the item type the payload belongs to.
	Type() ItemType
	// Validate checks the payload; failures wrap ErrInvalidSecret.
	Validate() error
}

// Credentials is a login and password pair.
type Credentials struct {
	// Login is the user name, email or phone the account is known by.
	Login string `json:"login,omitempty"`
	// Password is the password of the account.
	Password string `json:"password,omitempty"`
}

// Type returns ItemTypeCredentials.
func (Credentials) Type() ItemType { return ItemTypeCredentials }

// Validate requires the login or the password, or both.
func (c Credentials) Validate() error {
	if c.Login == "" && c.Password == "" {
		return fmt.Errorf("%w: both the login and the password are empty", ErrInvalidSecret)
	}

	return nil
}

// Text is arbitrary text.
type Text struct {
	// Content is the text itself, possibly of many lines.
	Content string `json:"content"`
}

// Type returns ItemTypeText.
func (Text) Type() ItemType { return ItemTypeText }

// Validate requires some text.
func (t Text) Validate() error {
	if t.Content == "" {
		return fmt.Errorf("%w: the text is empty", ErrInvalidSecret)
	}

	return nil
}

// Binary is arbitrary binary data, usually a file.
type Binary struct {
	// FileName is the name of the file the data came from, to save it back
	// under the same name.
	FileName string `json:"file_name,omitempty"`
	// Data is the content.
	Data []byte `json:"data"`
}

// BinaryFromFile reads a file into a Binary named after it. A file larger
// than MaxFileSize is refused before it is read.
func BinaryFromFile(path string) (Binary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Binary{}, err
	}
	if info.Size() > MaxFileSize {
		return Binary{}, fmt.Errorf("%w: %s is %d bytes, the limit is %d", ErrInvalidSecret, path, info.Size(), MaxFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Binary{}, err
	}

	return Binary{FileName: filepath.Base(path), Data: data}, nil
}

// Type returns ItemTypeBinary.
func (Binary) Type() ItemType { return ItemTypeBinary }

// Validate requires some data, up to MaxFileSize.
func (b Binary) Validate() error {
	switch {
	case len(b.Data) == 0:
		return fmt.Errorf("%w: the data is empty", ErrInvalidSecret)
	case len(b.Data) > MaxFileSize:
		return fmt.Errorf("%w: the data is %d bytes, the limit is %d", ErrInvalidSecret, len(b.Data), MaxFileSize)
	default:
		return nil
	}
}

// Card is bank card details.
type Card struct {
	// Number is the card number; NormalizeCardNumber removes the spaces and
	// dashes people type it with.
	Number string `json:"number"`
	// Holder is the name on the card.
	Holder string `json:"holder,omitempty"`
	// Expiry is the expiry date as MM/YY.
	Expiry string `json:"expiry,omitempty"`
	// CVV is the security code from the back of the card.
	CVV string `json:"cvv,omitempty"`
}

// Type returns ItemTypeCard.
func (Card) Type() ItemType { return ItemTypeCard }

// Validate checks the number with the Luhn algorithm, and the expiry date
// and the CVV if they are set.
func (c Card) Validate() error {
	number := NormalizeCardNumber(c.Number)

	switch {
	case len(number) < 12 || len(number) > 19 || !luhn.Valid(number):
		return fmt.Errorf("%w: %q is not a valid card number", ErrInvalidSecret, c.Number)
	case c.Expiry != "" && !expiryPattern.MatchString(c.Expiry):
		return fmt.Errorf("%w: the expiry date %q is not MM/YY", ErrInvalidSecret, c.Expiry)
	case c.CVV != "" && !cvvPattern.MatchString(c.CVV):
		return fmt.Errorf("%w: the CVV must be 3 or 4 digits", ErrInvalidSecret)
	default:
		return nil
	}
}

// DisplayNumber groups the digits of the card number by four. Unless reveal
// is set, only the last four digits are shown.
func (c Card) DisplayNumber(reveal bool) string {
	number := c.Number
	if !reveal {
		if len(number) <= 4 {
			return "****"
		}
		return "**** " + number[len(number)-4:]
	}

	var groups []string
	for len(number) > 4 {
		groups = append(groups, number[:4])
		number = number[4:]
	}

	return strings.Join(append(groups, number), " ")
}

// NormalizeCardNumber removes the spaces and dashes from a card number.
func NormalizeCardNumber(number string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(number)
}

// OTP is the settings of a generator of time-based one-time passwords.
type OTP struct {
	// Secret is the shared key in base32.
	Secret string `json:"secret"`
	// Issuer is the service the codes are for.
	Issuer string `json:"issuer,omitempty"`
	// Account is the account at the issuer.
	Account string `json:"account,omitempty"`
	// Algorithm is SHA1, SHA256 or SHA512; empty means SHA1.
	Algorithm string `json:"algorithm,omitempty"`
	// Digits is the length of a code; zero means 6.
	Digits int `json:"digits,omitempty"`
	// Period is how long a code lives in seconds; zero means 30.
	Period int `json:"period,omitempty"`
}

// Type returns ItemTypeOTP.
func (OTP) Type() ItemType { return ItemTypeOTP }

// Validate checks that codes can be generated.
func (o OTP) Validate() error {
	if err := o.Params().Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSecret, err)
	}

	return nil
}

// Params returns the settings in the form the otp package takes.
func (o OTP) Params() otp.Params {
	return otp.Params{
		Secret:    o.Secret,
		Issuer:    o.Issuer,
		Account:   o.Account,
		Algorithm: otp.Algorithm(o.Algorithm),
		Digits:    o.Digits,
		Period:    o.Period,
	}
}

// Code returns the code valid at t and how long it stays valid.
func (o OTP) Code(t time.Time) (string, time.Duration, error) {
	code, err := otp.Code(o.Params(), t)
	if err != nil {
		return "", 0, err
	}

	return code, otp.Remaining(o.Params(), t), nil
}

// OTPFromURI makes the settings out of an otpauth:// URI.
func OTPFromURI(uri string) (OTP, error) {
	p, err := otp.ParseURI(uri)
	if err != nil {
		return OTP{}, fmt.Errorf("%w: %w", ErrInvalidSecret, err)
	}

	return OTP{
		Secret:    p.Secret,
		Issuer:    p.Issuer,
		Account:   p.Account,
		Algorithm: string(p.Algorithm),
		Digits:    p.Digits,
		Period:    p.Period,
	}, nil
}

// secretJSON is how a secret is encoded before encryption. The payload stays
// raw until the type tells what to decode it into.
type secretJSON struct {
	Name     string          `json:"name"`
	Metadata string          `json:"metadata,omitempty"`
	Type     ItemType        `json:"type"`
	Payload  json.RawMessage `json:"payload"`
}

// MarshalJSON encodes the secret together with the type of its payload.
func (s Secret) MarshalJSON() ([]byte, error) {
	if s.Payload == nil {
		return nil, fmt.Errorf("%w: no content", ErrInvalidSecret)
	}

	payload, err := json.Marshal(s.Payload)
	if err != nil {
		return nil, err
	}

	return json.Marshal(secretJSON{
		Name:     s.Name,
		Metadata: s.Metadata,
		Type:     s.Payload.Type(),
		Payload:  payload,
	})
}

// UnmarshalJSON decodes what MarshalJSON has encoded.
func (s *Secret) UnmarshalJSON(data []byte) error {
	var raw secretJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var (
		payload Payload
		err     error
	)
	switch raw.Type {
	case ItemTypeCredentials:
		payload, err = decodePayload[Credentials](raw.Payload)
	case ItemTypeText:
		payload, err = decodePayload[Text](raw.Payload)
	case ItemTypeBinary:
		payload, err = decodePayload[Binary](raw.Payload)
	case ItemTypeCard:
		payload, err = decodePayload[Card](raw.Payload)
	case ItemTypeOTP:
		payload, err = decodePayload[OTP](raw.Payload)
	default:
		return fmt.Errorf("%w: %d", ErrUnknownType, raw.Type)
	}
	if err != nil {
		return fmt.Errorf("decode %s: %w", raw.Type, err)
	}

	*s = Secret{Name: raw.Name, Metadata: raw.Metadata, Payload: payload}

	return nil
}

// decodePayload decodes a payload of the type P.
func decodePayload[P Payload](data json.RawMessage) (Payload, error) {
	var payload P
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	return payload, nil
}
