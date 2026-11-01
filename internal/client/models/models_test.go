package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestItemType(t *testing.T) {
	for _, typ := range ItemTypes {
		parsed, err := ParseItemType(strings.ToUpper(typ.String()))
		require.NoError(t, err)
		assert.Equal(t, typ, parsed)
	}

	_, err := ParseItemType("wallet")
	assert.ErrorIs(t, err, ErrUnknownType)
	assert.Contains(t, err.Error(), "credentials, text, binary, card, otp")

	assert.Equal(t, "ItemType(42)", ItemType(42).String())
}

func TestSecret_JSONRoundTrip(t *testing.T) {
	secrets := []Secret{
		{Name: "GitHub", Metadata: "work account", Payload: Credentials{Login: "alice", Password: "s3cret"}},
		{Name: "Notes", Payload: Text{Content: "line 1\nline 2"}},
		{Name: "Key", Payload: Binary{FileName: "id_ed25519", Data: []byte{0, 1, 2, 255}}},
		{Name: "Visa", Metadata: "Bank of Gophers", Payload: Card{Number: "4111111111111111", Holder: "ALICE", Expiry: "12/30", CVV: "123"}},
		{Name: "GitHub 2FA", Payload: OTP{Secret: "JBSWY3DPEHPK3PXP", Issuer: "GitHub", Account: "alice"}},
	}

	for _, s := range secrets {
		data, err := json.Marshal(s)
		require.NoError(t, err)

		var decoded Secret
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, s, decoded)
		assert.Equal(t, s.Payload.Type(), decoded.Type())
	}
}

func TestSecret_JSONErrors(t *testing.T) {
	_, err := json.Marshal(Secret{Name: "empty"})
	assert.ErrorIs(t, err, ErrInvalidSecret)

	var s Secret
	assert.ErrorIs(t, json.Unmarshal([]byte(`{"name":"x","type":42,"payload":{}}`), &s), ErrUnknownType)
	assert.Error(t, json.Unmarshal([]byte(`{"name":"x","type":1,"payload":"not an object"}`), &s))
	assert.Error(t, json.Unmarshal([]byte(`not json`), &s))
}

func TestSecret_Validate(t *testing.T) {
	valid := []Secret{
		{Name: "only login", Payload: Credentials{Login: "alice"}},
		{Name: "only password", Payload: Credentials{Password: "wifi-password"}},
		{Name: "text", Payload: Text{Content: "x"}},
		{Name: "file", Payload: Binary{Data: []byte{1}}},
		{Name: "card with spaces", Payload: Card{Number: "4111 1111-1111 1111"}},
		{Name: "full card", Payload: Card{Number: "5555555555554444", Expiry: "01/29", CVV: "1234"}},
		{Name: "otp", Payload: OTP{Secret: "jbsw y3dp ehpk 3pxp", Digits: 8, Algorithm: "SHA256"}},
	}
	for _, s := range valid {
		assert.NoError(t, s.Validate(), s.Name)
	}

	invalid := []Secret{
		{Name: "", Payload: Text{Content: "x"}},
		{Name: "   ", Payload: Text{Content: "x"}},
		{Name: strings.Repeat("n", MaxNameLength+1), Payload: Text{Content: "x"}},
		{Name: "no payload"},
		{Name: "empty credentials", Payload: Credentials{}},
		{Name: "empty text", Payload: Text{}},
		{Name: "empty file", Payload: Binary{FileName: "empty.txt"}},
		{Name: "huge file", Payload: Binary{Data: make([]byte, MaxFileSize+1)}},
		{Name: "luhn", Payload: Card{Number: "4111111111111112"}},
		{Name: "short number", Payload: Card{Number: "42"}},
		{Name: "letters", Payload: Card{Number: "4111abcd11111111"}},
		{Name: "expiry", Payload: Card{Number: "4111111111111111", Expiry: "13/30"}},
		{Name: "cvv", Payload: Card{Number: "4111111111111111", CVV: "12"}},
		{Name: "otp secret", Payload: OTP{Secret: "not base32!"}},
		{Name: "otp digits", Payload: OTP{Secret: "JBSWY3DPEHPK3PXP", Digits: 4}},
	}
	for _, s := range invalid {
		assert.ErrorIs(t, s.Validate(), ErrInvalidSecret, s.Name)
	}

	assert.Equal(t, ItemType(0), Secret{}.Type())
}

func TestBinaryFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	require.NoError(t, os.WriteFile(path, []byte{1, 2, 3}, 0o600))

	b, err := BinaryFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, Binary{FileName: "id_ed25519", Data: []byte{1, 2, 3}}, b)

	_, err = BinaryFromFile(filepath.Join(dir, "missing"))
	assert.Error(t, err)

	huge := filepath.Join(dir, "huge")
	f, err := os.Create(huge)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(MaxFileSize+1))
	require.NoError(t, f.Close())
	_, err = BinaryFromFile(huge)
	assert.ErrorIs(t, err, ErrInvalidSecret)

	_, err = BinaryFromFile(dir)
	assert.Error(t, err, "a directory is not a file")
}

func TestNormalizeCardNumber(t *testing.T) {
	assert.Equal(t, "4111111111111111", NormalizeCardNumber("4111 1111-1111 1111"))
}

func TestCard_DisplayNumber(t *testing.T) {
	visa := Card{Number: "4111111111111111"}
	assert.Equal(t, "**** 1111", visa.DisplayNumber(false))
	assert.Equal(t, "4111 1111 1111 1111", visa.DisplayNumber(true))

	maestro := Card{Number: "6759649826438453"}
	assert.Equal(t, "6759 6498 2643 8453", maestro.DisplayNumber(true))

	odd := Card{Number: "4222222222222"}
	assert.Equal(t, "4222 2222 2222 2", odd.DisplayNumber(true))

	assert.Equal(t, "****", Card{Number: "123"}.DisplayNumber(false))
}

func TestOTP(t *testing.T) {
	o, err := OTPFromURI("otpauth://totp/GitHub:alice?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=GitHub")
	require.NoError(t, err)
	assert.Equal(t, OTP{Secret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", Issuer: "GitHub", Account: "alice", Algorithm: "SHA1", Digits: 6, Period: 30}, o)

	code, remaining, err := o.Code(time.Unix(59, 0))
	require.NoError(t, err)
	assert.Equal(t, "287082", code)
	assert.Equal(t, time.Second, remaining)

	_, err = OTPFromURI("https://example.com")
	assert.ErrorIs(t, err, ErrInvalidSecret)

	_, _, err = OTP{}.Code(time.Now())
	assert.Error(t, err)
}
