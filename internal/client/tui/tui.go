// Package tui is the interactive terminal interface of the GophKeeper client,
// built with Bubble Tea. It logs the user in, lists the entries with a
// filter, shows an entry with the secrets hidden until asked and the OTP
// code ticking, adds, edits and deletes entries, and syncs with the server
// in the background after every change.
package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/OneDayX/gophkeeper/internal/client/view"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// requestTimeout bounds one call to the server.
const requestTimeout = time.Minute

// Backend is what the TUI needs from the client; app.App provides it.
type Backend interface {
	// LoggedIn reports whether a user is logged in.
	LoggedIn() bool
	// User returns the login of the user.
	User() string
	// Server returns the address of the server.
	Server() string
	// Login logs in to an existing account.
	Login(ctx context.Context, login, password string) error
	// Register creates an account and logs in to it.
	Register(ctx context.Context, login, password string) error
	// Vault opens the vault of the logged-in user.
	Vault() (Vault, error)
}

// Vault is what the TUI needs from the vault; vault.Vault provides it.
type Vault interface {
	// List returns all entries, sorted by name.
	List() ([]vault.Entry, error)
	// Add stores a new secret.
	Add(s models.Secret) (vault.Entry, error)
	// Update replaces the secret of an entry.
	Update(id string, s models.Secret) (vault.Entry, error)
	// Delete deletes an entry.
	Delete(id string) error
	// Sync syncs the vault with the server.
	Sync(ctx context.Context) (vault.SyncResult, error)
}

// Run shows the TUI on the terminal until the user quits or ctx is done.
func Run(ctx context.Context, a *app.App) error {
	program := tea.NewProgram(New(appBackend{a}), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := program.Run()

	// A signal that cancels ctx is a way to quit, not a failure.
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}

	return err
}

// appBackend adapts app.App to Backend: its Vault returns the concrete type.
type appBackend struct {
	*app.App
}

// Vault opens the vault of the logged-in user.
func (b appBackend) Vault() (Vault, error) {
	v, err := b.App.Vault()
	if err != nil {
		// A nil *vault.Vault in the interface would not be nil.
		return nil, err
	}

	return v, nil
}

// screen is what the TUI shows.
type screen int

const (
	screenLogin screen = iota
	screenList
	screenPickType
	screenDetail
	screenForm
)

// Model is the state of the TUI.
type Model struct {
	backend Backend
	vault   Vault

	screen screen
	width  int
	height int

	// login is the form of the login screen.
	login loginForm

	// list shows the entries on the list screen.
	list list.Model
	// picker is the type chosen on the screen of a new entry.
	picker int

	// current is the entry on the detail screen, or the one being edited
	// or deleted.
	current vault.Entry
	// reveal shows the secrets of current.
	reveal bool
	// now is the time the OTP codes are shown for; ticks move it on.
	now   time.Time
	clock func() time.Time

	// form is the form of the entry being added or edited.
	form *entryForm

	// confirmDelete asks whether to delete current.
	confirmDelete bool
	// saving asks where to save the file of current; pathInput takes it.
	saving    bool
	pathInput textinput.Model

	// status is the line about the last operation; statusErr marks a
	// failure.
	status    string
	statusErr bool
}

// New returns the TUI working with the backend. If a user is logged in, it
// starts with their entries, otherwise with the login screen.
func New(backend Backend) Model {
	m := Model{
		backend: backend,
		list:    newList(),
		clock:   time.Now,
		login:   newLoginForm(),
	}
	m.now = m.clock()

	if backend.LoggedIn() {
		v, err := backend.Vault()
		if err != nil {
			m.setError(err)
			return m
		}
		m.vault = v
		m.screen = screenList
		m.setStatus("Syncing...")
	}

	return m
}

// Init loads the entries and syncs them, if a user is logged in.
func (m Model) Init() tea.Cmd {
	if m.vault == nil {
		return nil
	}

	return tea.Batch(loadCmd(m.vault), syncCmd(m.vault))
}

// Update handles a message: a key, a new terminal size or the result of a
// background operation.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, max(msg.Height-statusLines, 0))
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case authDoneMsg:
		return m.handleAuth(msg)

	case entriesMsg:
		return m.handleEntries(msg)

	case syncDoneMsg:
		if msg.err != nil {
			m.setError(fmt.Errorf("not synced: %w", msg.err))
			return m, nil
		}
		m.setStatus(view.Sync(msg.result))
		return m, loadCmd(m.vault)

	case savedMsg:
		return m.handleSaved(msg)

	case deletedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.setStatus("Deleted " + msg.name + ".")
		m.screen = screenList
		return m, tea.Batch(loadCmd(m.vault), syncCmd(m.vault))

	case fileSavedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.setStatus("Saved to " + msg.path + ".")
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		if m.showsOTP() {
			return m, tickCmd()
		}
		return m, nil
	}

	switch m.screen {
	case screenLogin:
		return m.updateLogin(msg)
	case screenList:
		return m.updateList(msg)
	case screenPickType:
		return m.updatePicker(msg)
	case screenDetail:
		return m.updateDetail(msg)
	case screenForm:
		return m.updateForm(msg)
	default:
		return m, nil
	}
}

// View draws the current screen with the status line and the key help.
func (m Model) View() string {
	var body string
	switch m.screen {
	case screenLogin:
		body = m.viewLogin()
	case screenList:
		body = m.viewList()
	case screenPickType:
		body = m.viewPicker()
	case screenDetail:
		body = m.viewDetail()
	case screenForm:
		body = m.viewForm()
	}

	return body + "\n" + m.viewStatus() + "\n" + helpStyle.Render(m.help())
}

// help lists the keys of the current screen.
func (m Model) help() string {
	switch {
	case m.confirmDelete:
		return "y delete · any other key keeps it"
	case m.saving:
		return "enter save · esc cancel"
	}

	switch m.screen {
	case screenLogin:
		mode := "register"
		if m.login.register {
			mode = "log in"
		}
		return "enter next/submit · tab move · ctrl+r " + mode + " instead · esc quit"
	case screenList:
		return "enter open · n new · e edit · d delete · s sync · / filter · q quit"
	case screenPickType:
		return "↑/↓ or 1-5 choose · enter continue · esc back"
	case screenDetail:
		keys := "r reveal · e edit · d delete"
		if _, ok := m.current.Secret.Payload.(models.Binary); ok {
			keys += " · w save file"
		}
		return keys + " · esc back"
	case screenForm:
		return "tab/shift+tab move · ctrl+s save · esc cancel"
	default:
		return ""
	}
}

// viewStatus draws the status line.
func (m Model) viewStatus() string {
	if m.statusErr {
		return errorStyle.Render(m.status)
	}

	return statusStyle.Render(m.status)
}

// setStatus shows a message about an operation.
func (m *Model) setStatus(text string) {
	m.status, m.statusErr = text, false
}

// setError shows a failure.
func (m *Model) setError(err error) {
	m.status, m.statusErr = "Error: "+err.Error(), true
}

// showsOTP reports whether the detail screen shows an OTP code, which needs
// a tick every second.
func (m Model) showsOTP() bool {
	_, ok := m.current.Secret.Payload.(models.OTP)
	return ok && m.screen == screenDetail
}
