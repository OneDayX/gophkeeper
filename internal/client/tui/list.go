package tui

import (
	"fmt"
	"strings"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// statusLines is how many lines below the list the status and the help take.
const statusLines = 3

// typeHints describe the types on the screen of a new entry.
var typeHints = map[models.ItemType]string{
	models.ItemTypeCredentials: "a login and a password",
	models.ItemTypeText:        "any text",
	models.ItemTypeBinary:      "a file, up to 8 MiB",
	models.ItemTypeCard:        "bank card details",
	models.ItemTypeOTP:         "one-time passwords from a TOTP secret",
}

// entryItem shows an entry in the list.
type entryItem struct {
	entry vault.Entry
}

// Title is the name of the entry.
func (i entryItem) Title() string {
	return i.entry.Secret.Name
}

// Description is the type of the entry, its sync state and the first line
// of its metadata.
func (i entryItem) Description() string {
	parts := []string{i.entry.Secret.Type().String()}
	if i.entry.Pending {
		parts = append(parts, "not synced")
	}
	if meta, _, _ := strings.Cut(i.entry.Secret.Metadata, "\n"); meta != "" {
		parts = append(parts, meta)
	}

	return strings.Join(parts, " · ")
}

// FilterValue is what the filter searches: the name, the type and the
// metadata, never the secrets.
func (i entryItem) FilterValue() string {
	return i.entry.Secret.Name + " " + i.entry.Secret.Type().String() + " " + i.entry.Secret.Metadata
}

// newList returns the list of entries; quitting and help are handled by the
// model.
func newList() list.Model {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = "GophKeeper"
	l.SetShowHelp(false)
	l.SetStatusBarItemName("entry", "entries")
	l.DisableQuitKeybindings()
	l.FilterInput.Cursor.SetMode(cursor.CursorStatic)

	return l
}

// handleEntries shows the entries loaded anew and keeps the open entry up to
// date with them.
func (m Model) handleEntries(msg entriesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.setError(msg.err)
		return m, nil
	}

	items := make([]list.Item, 0, len(msg.entries))
	stillThere := false
	for _, e := range msg.entries {
		items = append(items, entryItem{entry: e})
		if e.ID == m.current.ID {
			m.current = e
			stillThere = true
		}
	}

	// A sync may have deleted the open entry.
	if m.screen == screenDetail && !stillThere {
		m.screen = screenList
		m.setStatus(m.current.Secret.Name + " has been deleted on another device.")
	}

	return m, m.list.SetItems(items)
}

// selected returns the entry under the cursor.
func (m Model) selected() (vault.Entry, bool) {
	item, ok := m.list.SelectedItem().(entryItem)
	return item.entry, ok
}

// updateList handles the keys of the list screen.
func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if isKey && m.confirmDelete {
		return m.updateConfirm(key)
	}

	// While the filter is typed, every key belongs to it.
	if isKey && m.list.FilterState() != list.Filtering {
		switch key.String() {
		case "q":
			return m, tea.Quit
		case "enter":
			if e, ok := m.selected(); ok {
				return m.openDetail(e)
			}
			return m, nil
		case "n", "a":
			m.screen = screenPickType
			m.picker = 0
			return m, nil
		case "e":
			if e, ok := m.selected(); ok {
				m.current = e
				return m.openForm(e.Secret.Type(), &e)
			}
			return m, nil
		case "d":
			if e, ok := m.selected(); ok {
				m.current = e
				m.confirmDelete = true
			}
			return m, nil
		case "s":
			m.setStatus("Syncing...")
			return m, syncCmd(m.vault)
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)

	return m, cmd
}

// updateConfirm deletes the current entry on "y" and keeps it on any other
// key.
func (m Model) updateConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.confirmDelete = false
	if key.String() != "y" {
		return m, nil
	}

	m.setStatus("Deleting " + m.current.Secret.Name + "...")

	return m, deleteCmd(m.vault, m.current)
}

// viewList draws the list screen.
func (m Model) viewList() string {
	if m.confirmDelete {
		return m.list.View() + "\n" + errorStyle.Render(fmt.Sprintf("Delete %q on all devices? (y/N)", m.current.Secret.Name))
	}

	return m.list.View()
}

// updatePicker handles the keys of the screen of a new entry.
func (m Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	count := len(models.ItemTypes)
	switch k := key.String(); k {
	case "up", "k":
		m.picker = (m.picker + count - 1) % count
	case "down", "j":
		m.picker = (m.picker + 1) % count
	case "1", "2", "3", "4", "5":
		m.picker = int(k[0] - '1')
		return m.openForm(models.ItemTypes[m.picker], nil)
	case "enter":
		return m.openForm(models.ItemTypes[m.picker], nil)
	case "esc", "q":
		m.screen = screenList
	}

	return m, nil
}

// viewPicker draws the screen of a new entry.
func (m Model) viewPicker() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New entry · choose the type") + "\n\n")

	for i, t := range models.ItemTypes {
		line := fmt.Sprintf("%d. %-12s %s", i+1, t, typeHints[t])
		if i == m.picker {
			b.WriteString(focusStyle.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}

	return b.String()
}
