package tui

import (
	"fmt"
	"strings"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// field is one input of the entry form: a line, or an area for values of
// many lines such as the metadata.
type field struct {
	key       string
	label     string
	multiline bool
	input     textinput.Model
	area      textarea.Model
}

// value returns what the field holds.
func (f *field) value() string {
	if f.multiline {
		return f.area.Value()
	}

	return f.input.Value()
}

// focus puts the cursor into the field.
func (f *field) focus() {
	if f.multiline {
		f.area.Focus()
		return
	}
	f.input.Focus()
}

// blur takes the cursor out of the field.
func (f *field) blur() {
	if f.multiline {
		f.area.Blur()
		return
	}
	f.input.Blur()
}

// entryForm is the form of an entry being added or edited.
type entryForm struct {
	typ models.ItemType
	// id is the ID of the entry being edited, empty for a new one.
	id string
	// base is the secret being edited; the form keeps what it does not show,
	// such as the data of a file or the digits of an OTP code.
	base   models.Secret
	fields []*field
	focus  int
}

// newEntryForm returns the form for a new entry of the type, or for editing
// the entry if it is not nil.
func newEntryForm(t models.ItemType, editing *vault.Entry) *entryForm {
	f := &entryForm{typ: t}
	if editing != nil {
		f.id = editing.ID
		f.base = editing.Secret
	}
	s := f.base

	f.addLine("name", "Name", s.Name, false)
	switch t {
	case models.ItemTypeCredentials:
		p, _ := s.Payload.(models.Credentials)
		f.addLine("login", "Login", p.Login, false)
		f.addLine("password", "Password", p.Password, true)
	case models.ItemTypeText:
		p, _ := s.Payload.(models.Text)
		f.addArea("text", "Text", p.Content)
	case models.ItemTypeBinary:
		label := "Path of the file to keep"
		if p, ok := s.Payload.(models.Binary); ok {
			label = fmt.Sprintf("Path of a new file (now %s, %d bytes; leave empty to keep it)", p.FileName, len(p.Data))
		}
		f.addLine("file", label, "", false)
	case models.ItemTypeCard:
		p, _ := s.Payload.(models.Card)
		f.addLine("number", "Card number", p.Number, false)
		f.addLine("holder", "Holder", p.Holder, false)
		f.addLine("expiry", "Expiry (MM/YY)", p.Expiry, false)
		f.addLine("cvv", "CVV", p.CVV, true)
	case models.ItemTypeOTP:
		p, _ := s.Payload.(models.OTP)
		f.addLine("secret", "Secret (base32) or otpauth:// URI", p.Secret, true)
		f.addLine("issuer", "Issuer", p.Issuer, false)
		f.addLine("account", "Account", p.Account, false)
	}
	f.addArea("meta", "Metadata: site, bank, notes, activation codes", s.Metadata)

	f.fields[0].focus()

	return f
}

// addLine adds a one-line field; a secret one hides what is typed.
func (f *entryForm) addLine(key, label, value string, secret bool) {
	input := newInput("")
	input.SetValue(value)
	if secret {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
	}

	f.fields = append(f.fields, &field{key: key, label: label, input: input})
}

// addArea adds a field of many lines.
func (f *entryForm) addArea(key, label, value string) {
	area := textarea.New()
	area.ShowLineNumbers = false
	area.Prompt = "┃ "
	area.SetHeight(4)
	area.SetWidth(60)
	area.CharLimit = 0
	area.Cursor.SetMode(cursor.CursorStatic)
	area.SetValue(value)

	f.fields = append(f.fields, &field{key: key, label: label, multiline: true, area: area})
}

// value returns the value of the field with the key.
func (f *entryForm) value(key string) string {
	for _, fld := range f.fields {
		if fld.key == key {
			return fld.value()
		}
	}

	return ""
}

// move moves the focus by step, round the fields.
func (f *entryForm) move(step int) {
	f.fields[f.focus].blur()
	f.focus = (f.focus + step + len(f.fields)) % len(f.fields)
	f.fields[f.focus].focus()
}

// update passes a message to the field in focus.
func (f *entryForm) update(msg tea.Msg) tea.Cmd {
	fld := f.fields[f.focus]

	var cmd tea.Cmd
	if fld.multiline {
		fld.area, cmd = fld.area.Update(msg)
	} else {
		fld.input, cmd = fld.input.Update(msg)
	}

	return cmd
}

// secret builds the secret out of the form and checks it.
func (f *entryForm) secret() (models.Secret, error) {
	s := models.Secret{
		Name:     strings.TrimSpace(f.value("name")),
		Metadata: f.value("meta"),
	}

	switch f.typ {
	case models.ItemTypeCredentials:
		s.Payload = models.Credentials{Login: f.value("login"), Password: f.value("password")}

	case models.ItemTypeText:
		s.Payload = models.Text{Content: f.value("text")}

	case models.ItemTypeBinary:
		p, _ := f.base.Payload.(models.Binary)
		if path := strings.TrimSpace(f.value("file")); path != "" {
			var err error
			if p, err = models.BinaryFromFile(path); err != nil {
				return models.Secret{}, err
			}
		}
		s.Payload = p

	case models.ItemTypeCard:
		s.Payload = models.Card{
			Number: models.NormalizeCardNumber(f.value("number")),
			Holder: f.value("holder"),
			Expiry: strings.TrimSpace(f.value("expiry")),
			CVV:    strings.TrimSpace(f.value("cvv")),
		}

	case models.ItemTypeOTP:
		p, _ := f.base.Payload.(models.OTP)
		secret := strings.TrimSpace(f.value("secret"))
		if strings.HasPrefix(secret, "otpauth://") {
			var err error
			if p, err = models.OTPFromURI(secret); err != nil {
				return models.Secret{}, err
			}
		} else {
			p.Secret = secret
		}
		// The fields win over the URI, unless they are left empty.
		if issuer := f.value("issuer"); issuer != "" || !strings.HasPrefix(secret, "otpauth://") {
			p.Issuer = issuer
		}
		if account := f.value("account"); account != "" || !strings.HasPrefix(secret, "otpauth://") {
			p.Account = account
		}
		s.Payload = p
	}

	return s, s.Validate()
}

// openForm shows the form for a new entry of the type, or for editing the
// entry if it is not nil.
func (m Model) openForm(t models.ItemType, editing *vault.Entry) (tea.Model, tea.Cmd) {
	m.form = newEntryForm(t, editing)
	m.screen = screenForm
	m.setStatus("")

	return m, nil
}

// updateForm handles the keys of the form.
func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			if m.form.id != "" {
				m.screen = screenDetail
			} else {
				m.screen = screenList
			}
			m.form = nil
			return m, nil
		case "ctrl+s":
			s, err := m.form.secret()
			if err != nil {
				m.setError(err)
				return m, nil
			}
			m.setStatus("Saving...")
			return m, saveCmd(m.vault, m.form.id, s)
		case "tab":
			m.form.move(1)
			return m, nil
		case "shift+tab":
			m.form.move(-1)
			return m, nil
		case "enter":
			// Enter starts a new line in an area and moves on from a line.
			if !m.form.fields[m.form.focus].multiline {
				m.form.move(1)
				return m, nil
			}
		}
	}

	return m, m.form.update(msg)
}

// handleSaved shows the saved entry and syncs it.
func (m Model) handleSaved(msg savedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.setError(msg.err)
		return m, nil
	}

	m.form = nil
	m.setStatus("Saved " + msg.entry.Secret.Name + ". Syncing...")
	model, cmd := m.openDetail(msg.entry)

	return model, tea.Batch(cmd, loadCmd(m.vault), syncCmd(m.vault))
}

// viewForm draws the form.
func (m Model) viewForm() string {
	title := "New " + m.form.typ.String()
	if m.form.id != "" {
		title = "Edit " + m.form.base.Name
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")

	for i, fld := range m.form.fields {
		label := fld.label
		if i == m.form.focus {
			label = focusStyle.Render(label)
		} else {
			label = labelStyle.Render(label)
		}
		b.WriteString(label + "\n")

		if fld.multiline {
			b.WriteString(fld.area.View() + "\n\n")
		} else {
			b.WriteString(fld.input.View() + "\n\n")
		}
	}

	return b.String()
}
