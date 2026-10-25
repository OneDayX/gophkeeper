package otp

import (
	"encoding/base32"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCode_RFC6238 checks the test vectors of RFC 6238, appendix B.
func TestCode_RFC6238(t *testing.T) {
	secrets := map[Algorithm]string{
		SHA1:   base32.StdEncoding.EncodeToString([]byte("12345678901234567890")),
		SHA256: base32.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")),
		SHA512: base32.StdEncoding.EncodeToString([]byte("1234567890123456789012345678901234567890123456789012345678901234")),
	}

	vectors := []struct {
		unix int64
		want map[Algorithm]string
	}{
		{59, map[Algorithm]string{SHA1: "94287082", SHA256: "46119246", SHA512: "90693936"}},
		{1111111109, map[Algorithm]string{SHA1: "07081804", SHA256: "68084774", SHA512: "25091201"}},
		{1111111111, map[Algorithm]string{SHA1: "14050471", SHA256: "67062674", SHA512: "99943326"}},
		{1234567890, map[Algorithm]string{SHA1: "89005924", SHA256: "91819424", SHA512: "93441116"}},
		{2000000000, map[Algorithm]string{SHA1: "69279037", SHA256: "90698825", SHA512: "38618901"}},
		{20000000000, map[Algorithm]string{SHA1: "65353130", SHA256: "77737706", SHA512: "47863826"}},
	}

	for _, v := range vectors {
		for algorithm, want := range v.want {
			p := Params{Secret: secrets[algorithm], Algorithm: algorithm, Digits: 8}
			code, err := Code(p, time.Unix(v.unix, 0))
			require.NoError(t, err)
			assert.Equal(t, want, code, "%s at %d", algorithm, v.unix)
		}
	}
}

func TestCode_Defaults(t *testing.T) {
	// The secret as a service shows it: lowercase, in groups, no padding.
	p := Params{Secret: "gezd gnbv gy3t qojq gezd gnbv gy3t qojq"}

	code, err := Code(p, time.Unix(59, 0))
	require.NoError(t, err)
	assert.Equal(t, "287082", code, "six digits of the SHA1 vector")

	// The code stays the same within a period and changes with the next one.
	same, err := Code(p, time.Unix(31, 0))
	require.NoError(t, err)
	assert.Equal(t, code, same)
	next, err := Code(p, time.Unix(60, 0))
	require.NoError(t, err)
	assert.NotEqual(t, code, next)
}

func TestCode_Invalid(t *testing.T) {
	now := time.Now()

	for name, p := range map[string]Params{
		"empty secret":      {},
		"not base32":        {Secret: "not base32!"},
		"too short":         {Secret: "JBSWY3DPEHPK3PXP", Digits: 5},
		"too long":          {Secret: "JBSWY3DPEHPK3PXP", Digits: 9},
		"negative period":   {Secret: "JBSWY3DPEHPK3PXP", Period: -30},
		"unknown algorithm": {Secret: "JBSWY3DPEHPK3PXP", Algorithm: "MD5"},
	} {
		_, err := Code(p, now)
		assert.Error(t, err, name)
	}

	_, err := Code(Params{Secret: "JBSWY3DPEHPK3PXP"}, time.Unix(-1, 0))
	assert.ErrorIs(t, err, ErrInvalidParams)
}

func TestFormatCode(t *testing.T) {
	assert.Equal(t, "287 082", FormatCode("287082"))
	assert.Equal(t, "9428 7082", FormatCode("94287082"))
	assert.Equal(t, "123 4567", FormatCode("1234567"))
}

func TestRemaining(t *testing.T) {
	p := Params{Secret: "JBSWY3DPEHPK3PXP"}

	assert.Equal(t, 30*time.Second, Remaining(p, time.Unix(60, 0)))
	assert.Equal(t, 20*time.Second, Remaining(p, time.Unix(70, 0)))
	assert.Equal(t, 500*time.Millisecond, Remaining(p, time.Unix(89, 500_000_000)))
	assert.Equal(t, 50*time.Second, Remaining(Params{Period: 60}, time.Unix(70, 0)))
	assert.Equal(t, time.Duration(0), Remaining(Params{Period: -1}, time.Unix(70, 0)))
}

func TestParseURI(t *testing.T) {
	p, err := ParseURI("otpauth://totp/Example:alice@google.com?secret=JBSWY3DPEHPK3PXP&issuer=Example")
	require.NoError(t, err)
	assert.Equal(t, Params{
		Secret:    "JBSWY3DPEHPK3PXP",
		Issuer:    "Example",
		Account:   "alice@google.com",
		Algorithm: SHA1,
		Digits:    6,
		Period:    30,
	}, p)

	p, err = ParseURI("otpauth://totp/ACME%20Co:john.doe@email.com?secret=HXDMVJECJJWSRB3HWIZR4IFUGFTMXBOZ&algorithm=sha256&digits=8&period=60")
	require.NoError(t, err)
	assert.Equal(t, "ACME Co", p.Issuer, "the issuer from the label")
	assert.Equal(t, "john.doe@email.com", p.Account)
	assert.Equal(t, SHA256, p.Algorithm)
	assert.Equal(t, 8, p.Digits)
	assert.Equal(t, 60, p.Period)

	p, err = ParseURI("otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP")
	require.NoError(t, err)
	assert.Empty(t, p.Issuer)
	assert.Equal(t, "alice", p.Account)
}

func TestParseURI_Invalid(t *testing.T) {
	for _, uri := range []string{
		"https://example.com/?secret=JBSWY3DPEHPK3PXP",
		"otpauth://hotp/alice?secret=JBSWY3DPEHPK3PXP&counter=1",
		"otpauth://totp/alice",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&digits=six",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&period=soon",
		"otpauth://totp/alice?secret=JBSWY3DPEHPK3PXP&digits=10",
		"%%%",
	} {
		_, err := ParseURI(uri)
		assert.Error(t, err, uri)
	}
}
