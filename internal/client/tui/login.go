package tui

import (
	"errors"
	"strings"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// The inputs of the login form.
const (
	inputLogin = iota
	inputPassword
	// inputRepeat is shown only when registering.
	inputRepeat
)

// loginForm is the form of the login screen. When registering, it asks for
// the master password twice.
type loginForm struct {
	inputs   []textinput.Model
	focus    int
	register bool
}

// newLoginForm returns the form with the login in focus.
func newLoginForm() loginForm {
	login := newInput("login")
	password := newInput("master password")
	password.EchoMode = textinput.EchoPassword
	password.EchoCharacter = '•'
	repeat := newInput("the same master password")
	repeat.EchoMode = textinput.EchoPassword
	repeat.EchoCharacter = '•'

	f := loginForm{inputs: []textinput.Model{login, password, repeat}}
	f.inputs[inputLogin].Focus()

	return f
}

// visible returns how many inputs the current mode shows.
func (f *loginForm) visible() int {
	if f.register {
		return 3
	}

	return 2
}

// move moves the focus by step, round the visible inputs.
func (f *loginForm) move(step int) {
	f.inputs[f.focus].Blur()
	f.focus = (f.focus + step + f.visible()) % f.visible()
	f.inputs[f.focus].Focus()
}

// updateLogin handles the keys of the login screen.
func (m Model) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "esc":
		return m, tea.Quit
	case "ctrl+r":
		m.login.register = !m.login.register
		if m.login.focus >= m.login.visible() {
			m.login.move(1)
		}
		return m, nil
	case "tab", "down":
		m.login.move(1)
		return m, nil
	case "shift+tab", "up":
		m.login.move(-1)
		return m, nil
	case "enter":
		if m.login.focus < m.login.visible()-1 {
			m.login.move(1)
			return m, nil
		}
		return m.submitLogin()
	}

	var cmd tea.Cmd
	m.login.inputs[m.login.focus], cmd = m.login.inputs[m.login.focus].Update(msg)

	return m, cmd
}

// submitLogin checks the form and starts the login or the registration.
func (m Model) submitLogin() (tea.Model, tea.Cmd) {
	login := strings.TrimSpace(m.login.inputs[inputLogin].Value())
	password := m.login.inputs[inputPassword].Value()

	switch {
	case login == "" || password == "":
		m.setError(app.ErrEmptyCredentials)
		return m, nil
	case m.login.register && len([]rune(password)) < app.MinPasswordLength:
		m.setError(app.ErrWeakPassword)
		return m, nil
	case m.login.register && password != m.login.inputs[inputRepeat].Value():
		m.setError(errors.New("the passwords do not match"))
		return m, nil
	}

	if m.login.register {
		m.setStatus("Creating the account...")
	} else {
		m.setStatus("Logging in...")
	}

	return m, authCmd(m.backend, login, password, m.login.register)
}

// handleAuth opens the vault after a login, or shows why it has failed.
func (m Model) handleAuth(msg authDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.setError(msg.err)
		m.login.inputs[inputPassword].Reset()
		m.login.inputs[inputRepeat].Reset()
		return m, nil
	}

	v, err := m.backend.Vault()
	if err != nil {
		m.setError(err)
		return m, nil
	}

	m.vault = v
	m.screen = screenList
	m.login = newLoginForm() // the password does not stay in memory
	m.setStatus("Logged in as " + m.backend.User() + ". Syncing...")

	return m, tea.Batch(loadCmd(v), syncCmd(v))
}

// viewLogin draws the login screen.
func (m Model) viewLogin() string {
	title := "GophKeeper · log in"
	if m.login.register {
		title = "GophKeeper · create an account"
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n")
	b.WriteString(labelStyle.Render("Server: "+m.backend.Server()) + "\n\n")

	labels := []string{"Login", "Master password", "Repeat the master password"}
	for i := range m.login.visible() {
		b.WriteString(labelStyle.Render(labels[i]) + "\n")
		b.WriteString(m.login.inputs[i].View() + "\n\n")
	}

	if m.login.register {
		b.WriteString(labelStyle.Render("The master password never leaves this device and cannot be recovered.") + "\n")
	}

	return b.String()
}

// newInput returns a one-line input with a static cursor: a blinking one
// would redraw the screen twice a second for nothing.
func newInput(placeholder string) textinput.Model {
	input := textinput.New()
	input.Placeholder = placeholder
	input.Prompt = "> "
	input.Cursor.SetMode(cursor.CursorStatic)

	return input
}
