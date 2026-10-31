package tui

import (
	"path/filepath"
	"strings"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/OneDayX/gophkeeper/internal/client/view"
	tea "github.com/charmbracelet/bubbletea"
)

// openDetail shows an entry, with its secrets hidden. An OTP entry starts
// the clock that keeps its code fresh.
func (m Model) openDetail(e vault.Entry) (tea.Model, tea.Cmd) {
	m.current = e
	m.screen = screenDetail
	m.reveal = false
	m.now = m.clock()

	if m.showsOTP() {
		return m, tickCmd()
	}

	return m, nil
}

// updateDetail handles the keys of the detail screen.
func (m Model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.saving {
		return m.updateSavePath(key)
	}
	if m.confirmDelete {
		return m.updateConfirm(key)
	}

	switch key.String() {
	case "esc", "backspace", "q":
		m.screen = screenList
		m.reveal = false
	case "r":
		m.reveal = !m.reveal
	case "e":
		e := m.current
		return m.openForm(e.Secret.Type(), &e)
	case "d":
		m.confirmDelete = true
	case "w":
		if binary, ok := m.current.Secret.Payload.(models.Binary); ok {
			m.saving = true
			m.pathInput = newInput("path to save the file to")
			m.pathInput.SetValue(binary.FileName)
			m.pathInput.Focus()
		}
	}

	return m, nil
}

// updateSavePath handles the keys of the prompt for the path to save a file
// to.
func (m Model) updateSavePath(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.saving = false
		return m, nil
	case "enter":
		m.saving = false
		path := strings.TrimSpace(m.pathInput.Value())
		binary, _ := m.current.Secret.Payload.(models.Binary)
		if path == "" {
			path = binary.FileName
		}
		return m, writeFileCmd(filepath.Clean(path), binary.Data)
	}

	var cmd tea.Cmd
	m.pathInput, cmd = m.pathInput.Update(key)

	return m, cmd
}

// viewDetail draws the detail screen.
func (m Model) viewDetail() string {
	fields, masked := view.Fields(m.current, m.reveal, m.now)

	var b strings.Builder
	b.WriteString(titleStyle.Render(m.current.Secret.Name) + "\n\n")

	for _, f := range fields {
		if f.Label == "Name" || f.Multiline {
			continue
		}
		b.WriteString(labelStyle.Render(padLabel(f.Label)) + valueStyle(f.Label).Render(f.Value) + "\n")
	}
	for _, f := range fields {
		if f.Multiline {
			b.WriteString("\n" + labelStyle.Render(f.Label) + "\n" + f.Value + "\n")
		}
	}

	if masked {
		b.WriteString("\n" + labelStyle.Render("Secrets are hidden, r shows them.") + "\n")
	}

	switch {
	case m.confirmDelete:
		b.WriteString("\n" + errorStyle.Render("Delete \""+m.current.Secret.Name+"\" on all devices? (y/N)") + "\n")
	case m.saving:
		b.WriteString("\n" + labelStyle.Render("Save the file to") + "\n" + m.pathInput.View() + "\n")
	}

	return b.String()
}

// padLabel lines the values of the detail screen up.
func padLabel(label string) string {
	const width = 10
	if len(label) >= width {
		return label + " "
	}

	return label + strings.Repeat(" ", width-len(label))
}
