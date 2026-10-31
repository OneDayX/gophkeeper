package otp

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalidURI means the string is not an otpauth://totp/ URI.
var ErrInvalidURI = errors.New("invalid otpauth URI")

// ParseURI reads the settings of a generator from an otpauth URI, such as
// otpauth://totp/GitHub:alice?secret=JBSWY3DPEHPK3PXP&issuer=GitHub, the
// format of the QR codes the services show. Only TOTP is supported: the
// counter of HOTP would have to be synced between the devices with every
// code.
func ParseURI(uri string) (Params, error) {
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return Params{}, fmt.Errorf("%w: %w", ErrInvalidURI, err)
	}

	if u.Scheme != "otpauth" {
		return Params{}, fmt.Errorf("%w: the scheme is %q, not otpauth", ErrInvalidURI, u.Scheme)
	}
	if u.Host != "totp" {
		return Params{}, fmt.Errorf("%w: the type is %q, only totp is supported", ErrInvalidURI, u.Host)
	}

	// The label is "issuer:account" or just "account".
	label := strings.TrimPrefix(u.Path, "/")
	issuer, account, found := strings.Cut(label, ":")
	if !found {
		issuer, account = "", label
	}

	query := u.Query()
	p := Params{
		Secret:    query.Get("secret"),
		Issuer:    strings.TrimSpace(issuer),
		Account:   strings.TrimSpace(account),
		Algorithm: Algorithm(strings.ToUpper(query.Get("algorithm"))),
	}
	// The parameter is the newer way to name the issuer and wins.
	if value := query.Get("issuer"); value != "" {
		p.Issuer = value
	}

	if p.Digits, err = optionalInt(query, "digits"); err != nil {
		return Params{}, err
	}
	if p.Period, err = optionalInt(query, "period"); err != nil {
		return Params{}, err
	}

	if err := p.Validate(); err != nil {
		return Params{}, err
	}

	return p.WithDefaults(), nil
}

// optionalInt reads a number from the query, zero if it is not there.
func optionalInt(query url.Values, key string) (int, error) {
	value := query.Get(key)
	if value == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %s is %q, not a number", ErrInvalidURI, key, value)
	}

	return n, nil
}
