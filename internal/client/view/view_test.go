package view

import (
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/stretchr/testify/assert"
)

// values maps the labels of the fields to their values.
func values(fields []Field) map[string]string {
	m := make(map[string]string, len(fields))
	for _, f := range fields {
		m[f.Label] = f.Value
	}

	return m
}

func entry(s models.Secret) vault.Entry {
	return vault.Entry{ID: "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e01", Secret: s, UpdatedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)}
}

func TestFields_Credentials(t *testing.T) {
	e := entry(models.Secret{Name: "GitHub", Metadata: "work", Payload: models.Credentials{Login: "alice", Password: "s3cret"}})

	fields, masked := Fields(e, false, time.Now())
	assert.True(t, masked)
	got := values(fields)
	assert.Equal(t, "alice", got["Login"])
	assert.Equal(t, Hidden, got["Password"])
	assert.Equal(t, "credentials", got["Type"])
	assert.Equal(t, "2026-10-25 12:00, synced", got["Updated"])
	assert.Equal(t, "work", got["Metadata"])
	assert.True(t, fields[len(fields)-1].Multiline, "the metadata goes last, as a block")

	fields, masked = Fields(e, true, time.Now())
	assert.False(t, masked)
	assert.Equal(t, "s3cret", values(fields)["Password"])
}

func TestFields_Card(t *testing.T) {
	e := entry(models.Secret{Name: "Visa", Payload: models.Card{Number: "4111111111111111", Holder: "ALICE", Expiry: "12/30", CVV: "123"}})
	e.Pending = true

	fields, masked := Fields(e, false, time.Now())
	assert.True(t, masked)
	got := values(fields)
	assert.Equal(t, "**** 1111", got["Number"])
	assert.Equal(t, Hidden, got["CVV"])
	assert.Equal(t, "12/30", got["Expiry"])
	assert.Contains(t, got["Updated"], "not synced")

	fields, _ = Fields(e, true, time.Now())
	assert.Equal(t, "4111 1111 1111 1111", values(fields)["Number"])
	assert.Equal(t, "123", values(fields)["CVV"])
}

func TestFields_OTP(t *testing.T) {
	e := entry(models.Secret{Name: "2FA", Payload: models.OTP{Secret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", Issuer: "GitHub"}})

	fields, masked := Fields(e, false, time.Unix(59, 0))
	assert.True(t, masked)
	got := values(fields)
	assert.Equal(t, "287 082 (valid for 1s)", got["Code"])
	assert.Equal(t, "GitHub", got["Issuer"])
	assert.Equal(t, Hidden, got["Secret"])

	broken := entry(models.Secret{Name: "2FA", Payload: models.OTP{Secret: "not base32!"}})
	fields, _ = Fields(broken, false, time.Now())
	assert.Contains(t, values(fields)["Code"], "cannot generate")
}

func TestFields_TextAndBinary(t *testing.T) {
	fields, masked := Fields(entry(models.Secret{Name: "Notes", Payload: models.Text{Content: "a\nb"}}), false, time.Now())
	assert.False(t, masked, "a text has nothing to hide behind --reveal")
	last := fields[len(fields)-1]
	assert.Equal(t, Field{Label: "Text", Value: "a\nb", Multiline: true}, last)

	fields, _ = Fields(entry(models.Secret{Name: "Key", Payload: models.Binary{FileName: "id_ed25519", Data: []byte{1, 2}}}), false, time.Now())
	got := values(fields)
	assert.Equal(t, "id_ed25519", got["File"])
	assert.Equal(t, "2 bytes", got["Size"])
}

func TestRemaining(t *testing.T) {
	assert.Equal(t, "17s", Remaining(17*time.Second))
	assert.Equal(t, "1s", Remaining(100*time.Millisecond), "rounded up")
}

func TestSync(t *testing.T) {
	assert.Equal(t, "Synced, everything is up to date.", Sync(vault.SyncResult{}))
	assert.Equal(t, "Synced: 2 received, 1 sent.", Sync(vault.SyncResult{Pulled: 2, Pushed: 1}))
	assert.Contains(t, Sync(vault.SyncResult{Pulled: 1, Conflicts: 1}), "1 entry was changed on another device too; your version is kept as a copy")
	assert.Contains(t, Sync(vault.SyncResult{Pulled: 2, Conflicts: 2}), "2 entries were changed on another device too; your versions are kept as copies")
}
