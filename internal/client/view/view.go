// Package view turns entries into the labelled fields that the CLI and the
// TUI show, so that both hide the same secrets in the same way, and formats
// the rest of what they print.
package view

import (
	"fmt"
	"math"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/OneDayX/gophkeeper/internal/otp"
)

// Hidden replaces a secret value that is not revealed.
const Hidden = "********"

// Field is one labelled value of an entry.
type Field struct {
	// Label names the value, such as "Password".
	Label string
	// Value is the text to show.
	Value string
	// Multiline marks a value of many lines, shown as a block below the
	// other fields.
	Multiline bool
}

// Fields returns the non-empty fields of an entry in the order to show
// them. Secret values are replaced with Hidden unless reveal is set; masked
// reports whether any value was hidden. An OTP entry shows the code valid
// at now.
func Fields(e vault.Entry, reveal bool, now time.Time) (fields []Field, masked bool) {
	add := func(label, value string) {
		if value != "" {
			fields = append(fields, Field{Label: label, Value: value})
		}
	}
	addSecret := func(label, value string) {
		if value != "" && !reveal {
			value = Hidden
			masked = true
		}
		add(label, value)
	}

	add("Name", e.Secret.Name)
	add("Type", e.Secret.Type().String())

	switch p := e.Secret.Payload.(type) {
	case models.Credentials:
		add("Login", p.Login)
		addSecret("Password", p.Password)
	case models.Binary:
		add("File", p.FileName)
		add("Size", fmt.Sprintf("%d bytes", len(p.Data)))
	case models.Card:
		add("Number", p.DisplayNumber(reveal))
		masked = masked || !reveal
		add("Holder", p.Holder)
		add("Expiry", p.Expiry)
		addSecret("CVV", p.CVV)
	case models.OTP:
		code, remaining, err := p.Code(now)
		if err != nil {
			add("Code", "cannot generate: "+err.Error())
		} else {
			add("Code", fmt.Sprintf("%s (valid for %s)", otp.FormatCode(code), Remaining(remaining)))
		}
		add("Issuer", p.Issuer)
		add("Account", p.Account)
		addSecret("Secret", p.Secret)
	}

	state := "synced"
	if e.Pending {
		state = "not synced"
	}
	add("Updated", Time(e.UpdatedAt)+", "+state)
	add("ID", e.ID)

	if text, ok := e.Secret.Payload.(models.Text); ok && text.Content != "" {
		fields = append(fields, Field{Label: "Text", Value: text.Content, Multiline: true})
	}
	if e.Secret.Metadata != "" {
		fields = append(fields, Field{Label: "Metadata", Value: e.Secret.Metadata, Multiline: true})
	}

	return fields, masked
}

// Remaining shows how long a code stays valid, in whole seconds rounded up,
// so that a valid code never shows 0s.
func Remaining(d time.Duration) string {
	return fmt.Sprintf("%ds", int(math.Ceil(d.Seconds())))
}

// Time shows a time in the local zone, to the minute.
func Time(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04")
}

// Sync tells what a sync has done.
func Sync(r vault.SyncResult) string {
	if r == (vault.SyncResult{}) {
		return "Synced, everything is up to date."
	}

	text := fmt.Sprintf("Synced: %d received, %d sent.", r.Pulled, r.Pushed)
	if r.Conflicts > 0 {
		text += fmt.Sprintf(" %d entries were changed on another device too; your versions are kept as copies marked \"conflict\".", r.Conflicts)
	}

	return text
}
