package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBackend plays the client app.
type fakeBackend struct {
	loggedIn   bool
	user       string
	registered bool
	authErr    error
	vault      *fakeVault
	vaultErr   error
}

func (b *fakeBackend) LoggedIn() bool { return b.loggedIn }
func (b *fakeBackend) User() string   { return b.user }
func (b *fakeBackend) Server() string { return "localhost:3200" }

func (b *fakeBackend) Login(_ context.Context, login, _ string) error {
	if b.authErr != nil {
		return b.authErr
	}
	b.loggedIn, b.user = true, login
	return nil
}

func (b *fakeBackend) Register(ctx context.Context, login, password string) error {
	b.registered = true
	return b.Login(ctx, login, password)
}

func (b *fakeBackend) Vault() (Vault, error) {
	if b.vaultErr != nil {
		return nil, b.vaultErr
	}
	return b.vault, nil
}

// fakeVault keeps the entries in memory; the commands run it from other
// goroutines, hence the lock.
type fakeVault struct {
	mu      sync.Mutex
	entries []vault.Entry
	syncs   int
	syncErr error
	lastID  int
}

func (v *fakeVault) List() ([]vault.Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.entries), nil
}

func (v *fakeVault) Add(s models.Secret) (vault.Entry, error) {
	if err := s.Validate(); err != nil {
		return vault.Entry{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lastID++
	e := vault.Entry{ID: fmt.Sprintf("%08d-0000-4000-8000-000000000000", v.lastID), Secret: s, Pending: true}
	v.entries = append(v.entries, e)
	return e, nil
}

func (v *fakeVault) Update(id string, s models.Secret) (vault.Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, e := range v.entries {
		if e.ID == id {
			v.entries[i].Secret, v.entries[i].Pending = s, true
			return v.entries[i], nil
		}
	}
	return vault.Entry{}, models.ErrNotFound
}

func (v *fakeVault) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, e := range v.entries {
		if e.ID == id {
			v.entries = slices.Delete(v.entries, i, i+1)
			return nil
		}
	}
	return models.ErrNotFound
}

func (v *fakeVault) Sync(context.Context) (vault.SyncResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.syncs++
	if v.syncErr != nil {
		return vault.SyncResult{}, v.syncErr
	}
	pushed := 0
	for i := range v.entries {
		if v.entries[i].Pending {
			v.entries[i].Pending = false
			pushed++
		}
	}
	return vault.SyncResult{Pushed: pushed}, nil
}

func (v *fakeVault) syncCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.syncs
}

// sampleVault holds an entry of every type but binary.
func sampleVault() *fakeVault {
	return &fakeVault{entries: []vault.Entry{
		{ID: "11111111-0000-4000-8000-000000000000", Secret: models.Secret{Name: "GitHub", Metadata: "github.com\nwork", Payload: models.Credentials{Login: "alice", Password: "s3cret"}}},
		{ID: "22222222-0000-4000-8000-000000000000", Secret: models.Secret{Name: "Visa", Payload: models.Card{Number: "4111111111111111", CVV: "123"}}},
		{ID: "33333333-0000-4000-8000-000000000000", Secret: models.Secret{Name: "2FA", Payload: models.OTP{Secret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"}}},
		{ID: "44444444-0000-4000-8000-000000000000", Secret: models.Secret{Name: "Notes", Payload: models.Text{Content: "dear diary"}}},
	}}
}

// started returns a model of a logged-in user after its first commands
// have run, on a terminal of a usual size.
func started(t *testing.T, v *fakeVault) Model {
	t.Helper()

	m := New(&fakeBackend{loggedIn: true, user: "alice", vault: v})
	m = run(t, m, m.Init())

	return send(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
}

// key returns the message of a key, named like tea.KeyMsg.String names it.
func key(name string) tea.KeyMsg {
	types := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, "backspace": tea.KeyBackspace,
		"ctrl+s": tea.KeyCtrlS, "ctrl+r": tea.KeyCtrlR, "ctrl+c": tea.KeyCtrlC, "ctrl+u": tea.KeyCtrlU,
	}
	if typ, ok := types[name]; ok {
		return tea.KeyMsg{Type: typ}
	}

	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// press sends the keys one by one.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()

	for _, k := range keys {
		m = send(t, m, key(k))
	}

	return m
}

// send passes a message to the model and runs the commands it returns.
func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()

	next, cmd := m.Update(msg)

	return run(t, next.(Model), cmd)
}

// run runs a command and feeds its messages back into the model, as the
// Bubble Tea runtime does. A command that does not finish at once is a
// timer, such as the OTP tick, and is left alone.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()

	if cmd == nil {
		return m
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return m
	}

	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
		return m
	case tea.BatchMsg:
		for _, c := range msg {
			m = run(t, m, c)
		}
		return m
	default:
		return send(t, m, msg)
	}
}

// quits reports whether the key makes the model quit.
func quits(m Model, k string) bool {
	_, cmd := m.Update(key(k))
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestLogin(t *testing.T) {
	v := sampleVault()
	backend := &fakeBackend{vault: v}
	m := New(backend)
	assert.Nil(t, m.Init(), "nothing to load before the login")
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	assert.Equal(t, screenLogin, m.screen)
	assert.Contains(t, m.View(), "log in")
	assert.Contains(t, m.View(), "localhost:3200")

	// Both fields are needed.
	m = press(t, m, "enter", "enter")
	assert.True(t, m.statusErr)

	m = press(t, m, "shift+tab", "alice", "tab", "master password", "enter")
	assert.Equal(t, screenList, m.screen)
	assert.Equal(t, "alice", backend.user)
	assert.Equal(t, 1, v.syncCount(), "a sync right after the login")
	assert.Contains(t, m.View(), "GitHub")
	assert.Empty(t, m.login.inputs[inputPassword].Value(), "the password is forgotten")
}

func TestLogin_Failure(t *testing.T) {
	backend := &fakeBackend{vault: sampleVault(), authErr: models.ErrInvalidCredentials}
	m := New(backend)

	m = press(t, m, "alice", "enter", "wrong", "enter")
	assert.Equal(t, screenLogin, m.screen)
	assert.True(t, m.statusErr)
	assert.Contains(t, m.View(), "invalid login or password")
	assert.Empty(t, m.login.inputs[inputPassword].Value())

	backend.authErr = nil
	backend.vaultErr = errors.New("vault is locked")
	m = press(t, m, "right password", "enter")
	assert.Equal(t, screenLogin, m.screen)
	assert.Contains(t, m.View(), "vault is locked")

	assert.True(t, quits(m, "esc"))
}

func TestRegister(t *testing.T) {
	backend := &fakeBackend{vault: &fakeVault{}}
	m := New(backend)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})

	m = press(t, m, "ctrl+r")
	assert.True(t, m.login.register)
	assert.Contains(t, m.View(), "create an account")
	assert.Contains(t, m.View(), "Repeat the master password")

	m = press(t, m, "bob", "tab", "short", "tab", "short", "enter")
	assert.Contains(t, m.status, app.ErrWeakPassword.Error())

	m = press(t, m, "up", "ctrl+u", "long enough", "down", "ctrl+u", "different!", "enter")
	assert.Contains(t, m.status, "do not match")
	assert.False(t, backend.registered)

	m = press(t, m, "ctrl+u", "long enough", "enter")
	assert.True(t, backend.registered)
	assert.Equal(t, screenList, m.screen)
	assert.Contains(t, m.View(), "No entries", "the list of a new account is empty")

	// Switching back hides the third input, moving the focus off it.
	m2 := New(&fakeBackend{vault: &fakeVault{}})
	m2 = press(t, m2, "ctrl+r", "tab", "tab", "ctrl+r")
	assert.Less(t, m2.login.focus, 2)
}

func TestStart_LoggedIn(t *testing.T) {
	v := sampleVault()
	m := started(t, v)

	assert.Equal(t, screenList, m.screen)
	assert.Equal(t, 1, v.syncCount())
	view := m.View()
	for _, name := range []string{"GitHub", "Visa", "2FA", "Notes"} {
		assert.Contains(t, view, name)
	}
	assert.Contains(t, view, "Synced")

	broken := New(&fakeBackend{loggedIn: true, vaultErr: errors.New("vault is locked")})
	assert.Equal(t, screenLogin, broken.screen)
	assert.Contains(t, broken.View(), "vault is locked")
}

func TestDetail(t *testing.T) {
	m := started(t, sampleVault())

	m = press(t, m, "enter")
	require.Equal(t, screenDetail, m.screen)
	assert.Equal(t, "GitHub", m.current.Secret.Name)
	view := m.View()
	assert.Contains(t, view, "alice")
	assert.NotContains(t, view, "s3cret")
	assert.Contains(t, view, "work", "the metadata")

	m = press(t, m, "r")
	assert.Contains(t, m.View(), "s3cret")

	m = press(t, m, "esc")
	assert.Equal(t, screenList, m.screen)
	assert.False(t, m.reveal, "revealing does not stick")

	// The second entry is the card.
	m = press(t, m, "down", "enter")
	assert.Contains(t, m.View(), "**** 1111")
	assert.Contains(t, m.help(), "r reveal")
	assert.NotContains(t, m.help(), "save file")
}

func TestDetail_OTPTicks(t *testing.T) {
	m := started(t, sampleVault())
	m.clock = func() time.Time { return time.Unix(59, 0) }

	m = press(t, m, "down", "down")
	next, cmd := m.Update(key("enter"))
	m = next.(Model)
	require.NotNil(t, cmd, "an OTP entry starts the clock")
	assert.Contains(t, m.View(), "287 082")

	next, cmd = m.Update(tickMsg(time.Unix(61, 0)))
	m = next.(Model)
	assert.NotNil(t, cmd, "the clock goes on")
	assert.NotContains(t, m.View(), "287 082", "a new period, a new code")

	m = press(t, m, "esc")
	_, cmd = m.Update(tickMsg(time.Unix(62, 0)))
	assert.Nil(t, cmd, "the clock stops off the OTP entry")
}

func TestAdd(t *testing.T) {
	v := sampleVault()
	m := started(t, v)

	m = press(t, m, "n")
	assert.Equal(t, screenPickType, m.screen)
	assert.Contains(t, m.View(), "credentials")

	m = press(t, m, "down", "up", "up", "esc")
	assert.Equal(t, screenList, m.screen)

	// Text is the second type.
	m = press(t, m, "a", "down", "enter")
	require.Equal(t, screenForm, m.screen)
	assert.Contains(t, m.View(), "New text")

	m = press(t, m, "Shopping", "enter", "milk", "enter", "eggs", "tab", "groceries")
	m = press(t, m, "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	assert.Equal(t, "Shopping", m.current.Secret.Name)
	assert.Equal(t, models.Text{Content: "milk\neggs"}, m.current.Secret.Payload)
	assert.Equal(t, "groceries", m.current.Secret.Metadata)
	assert.Equal(t, 2, v.syncCount(), "a sync after the change")
	assert.Len(t, v.entries, 5)
}

func TestAdd_AllTypes(t *testing.T) {
	v := &fakeVault{}
	m := started(t, v)

	m = press(t, m, "n", "1", "GitHub", "tab", "alice", "tab", "s3cret", "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	assert.Equal(t, models.Credentials{Login: "alice", Password: "s3cret"}, m.current.Secret.Payload)

	m = press(t, m, "esc", "n", "4", "Visa", "tab", "4111 1111 1111 1111", "tab", "ALICE", "tab", "12/30", "tab", "123", "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	assert.Equal(t, models.Card{Number: "4111111111111111", Holder: "ALICE", Expiry: "12/30", CVV: "123"}, m.current.Secret.Payload)

	m = press(t, m, "esc", "n", "5", "2FA", "tab", "otpauth://totp/GitHub:alice?secret=JBSWY3DPEHPK3PXP&issuer=GitHub&digits=8", "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	generator := m.current.Secret.Payload.(models.OTP)
	assert.Equal(t, "GitHub", generator.Issuer, "from the URI")
	assert.Equal(t, 8, generator.Digits)

	file := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(file, []byte("private key"), 0o600))
	m = press(t, m, "esc", "n", "3", "SSH key", "tab", file, "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	assert.Equal(t, models.Binary{FileName: "id_ed25519", Data: []byte("private key")}, m.current.Secret.Payload)
	assert.Contains(t, m.help(), "save file")

	assert.Len(t, v.entries, 4)
}

func TestForm_Errors(t *testing.T) {
	m := started(t, &fakeVault{})

	m = press(t, m, "n", "4", "Visa", "tab", "4111 1111 1111 1112", "ctrl+s")
	assert.Equal(t, screenForm, m.screen, "an invalid card stays in the form")
	assert.True(t, m.statusErr)
	assert.Contains(t, m.status, "not a valid card number")

	m = press(t, m, "esc")
	assert.Equal(t, screenList, m.screen)

	m = press(t, m, "n", "3", "Missing", "tab", filepath.Join(t.TempDir(), "missing"), "ctrl+s")
	assert.Equal(t, screenForm, m.screen)
	assert.True(t, m.statusErr)

	m = press(t, m, "esc", "n", "5", "Bad 2FA", "tab", "otpauth://hotp/x?secret=JBSWY3DPEHPK3PXP", "ctrl+s")
	assert.Equal(t, screenForm, m.screen)
	assert.True(t, m.statusErr)
}

func TestEdit(t *testing.T) {
	v := sampleVault()
	m := started(t, v)

	// From the list: rename GitHub.
	m = press(t, m, "e")
	require.Equal(t, screenForm, m.screen)
	assert.Contains(t, m.View(), "Edit GitHub")
	assert.Equal(t, "alice", m.form.value("login"), "the form is filled in")

	m = press(t, m, "ctrl+u", "GitHub (work)", "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	assert.Equal(t, "GitHub (work)", v.entries[0].Secret.Name)
	assert.Equal(t, models.Credentials{Login: "alice", Password: "s3cret"}, v.entries[0].Secret.Payload)

	// From the detail screen; esc goes back to it.
	m = press(t, m, "e", "esc")
	assert.Equal(t, screenDetail, m.screen)

	// The OTP settings the form does not show are kept. The fields are the
	// name, the secret, the issuer, the account and the metadata.
	m = press(t, m, "esc", "down", "down", "enter", "e", "tab", "tab", "GitHub", "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	generator := v.entries[2].Secret.Payload.(models.OTP)
	assert.Equal(t, "GitHub", generator.Issuer)
	assert.Equal(t, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", generator.Secret)
}

func TestEdit_BinaryKeepsData(t *testing.T) {
	v := &fakeVault{entries: []vault.Entry{{
		ID:     "55555555-0000-4000-8000-000000000000",
		Secret: models.Secret{Name: "Key", Payload: models.Binary{FileName: "id_ed25519", Data: []byte("private key")}},
	}}}
	m := started(t, v)

	m = press(t, m, "e")
	assert.Contains(t, m.View(), "now id_ed25519, 11 bytes")
	m = press(t, m, "tab", "tab", "a note", "ctrl+s")
	require.Equal(t, screenDetail, m.screen, m.status)
	assert.Equal(t, []byte("private key"), v.entries[0].Secret.Payload.(models.Binary).Data)
	assert.Equal(t, "a note", v.entries[0].Secret.Metadata)
}

func TestDelete(t *testing.T) {
	v := sampleVault()
	m := started(t, v)

	m = press(t, m, "d")
	assert.Contains(t, m.View(), `Delete "GitHub" on all devices?`)
	assert.Contains(t, m.help(), "y delete")
	m = press(t, m, "n")
	assert.Len(t, v.entries, 4, "any other key keeps it")

	// The status tells about the deletion until the sync after it reports.
	next, _ := m.Update(key("d"))
	next, cmd := next.(Model).Update(key("y"))
	m = next.(Model)
	assert.Contains(t, m.status, "Deleting GitHub")
	m = run(t, m, cmd)
	assert.Len(t, v.entries, 3)
	assert.NotContains(t, m.View(), "GitHub")

	// From the detail screen.
	m = press(t, m, "enter", "d", "y")
	assert.Equal(t, screenList, m.screen)
	assert.Len(t, v.entries, 2)
}

func TestSaveFile(t *testing.T) {
	v := &fakeVault{entries: []vault.Entry{{
		ID:     "55555555-0000-4000-8000-000000000000",
		Secret: models.Secret{Name: "Key", Payload: models.Binary{FileName: "id_ed25519", Data: []byte("private key")}},
	}}}
	m := started(t, v)

	path := filepath.Join(t.TempDir(), "restored")
	m = press(t, m, "enter", "w")
	require.True(t, m.saving)
	assert.Contains(t, m.View(), "Save the file to")
	assert.Equal(t, "id_ed25519", m.pathInput.Value(), "the file name is offered")

	m = press(t, m, "esc")
	assert.False(t, m.saving)

	m = press(t, m, "w", "ctrl+u", path, "enter")
	assert.Contains(t, m.status, "Saved to")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "private key", string(data))

	m = press(t, m, "w", "ctrl+u", filepath.Join(t.TempDir(), "no", "such", "dir"), "enter")
	assert.True(t, m.statusErr)
}

func TestSync(t *testing.T) {
	v := sampleVault()
	m := started(t, v)

	m = press(t, m, "s")
	assert.Equal(t, 2, v.syncCount())

	v.syncErr = models.ErrUnavailable
	m = press(t, m, "s")
	assert.True(t, m.statusErr)
	assert.Contains(t, m.status, "not synced")
}

func TestEntryDeletedElsewhere(t *testing.T) {
	v := sampleVault()
	m := started(t, v)
	m = press(t, m, "enter")

	// Another device deletes the open entry; the next load notices.
	require.NoError(t, v.Delete(m.current.ID))
	m = run(t, m, loadCmd(v))
	assert.Equal(t, screenList, m.screen)
	assert.Contains(t, m.status, "deleted on another device")

	m = send(t, m, entriesMsg{err: errors.New("disk is broken")})
	assert.True(t, m.statusErr)

	m = send(t, m, deletedMsg{err: errors.New("disk is broken")})
	assert.Contains(t, m.status, "disk is broken")

	m = send(t, m, savedMsg{err: errors.New("disk is full")})
	assert.Contains(t, m.status, "disk is full")
}

func TestQuit(t *testing.T) {
	m := started(t, sampleVault())

	assert.True(t, quits(m, "q"))
	assert.True(t, quits(m, "ctrl+c"))
	assert.False(t, quits(m, "x"))

	// While the filter is typed, q is a letter.
	filtering := press(t, m, "/")
	assert.False(t, quits(filtering, "q"))
}

func TestFilter(t *testing.T) {
	m := started(t, sampleVault())

	m = press(t, m, "/", "v", "i", "s", "enter")
	view := m.View()
	assert.Contains(t, view, "Visa")
	assert.NotContains(t, view, "GitHub")
}

func TestHelpAndSmallTerminal(t *testing.T) {
	m := started(t, sampleVault())
	m = send(t, m, tea.WindowSizeMsg{Width: 20, Height: 2})

	assert.NotPanics(t, func() { _ = m.View() })
	assert.Contains(t, m.help(), "q quit")

	m = press(t, m, "n")
	assert.Contains(t, m.help(), "1-5")
	m = press(t, m, "1")
	assert.Contains(t, m.help(), "ctrl+s save")
	assert.Equal(t, "", Model{screen: screen(42)}.help())
}

func TestAppBackend(t *testing.T) {
	a, err := app.New(app.Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer a.Close()

	v, err := appBackend{a}.Vault()
	assert.ErrorIs(t, err, models.ErrNotLoggedIn)
	assert.Nil(t, v, "no typed nil in the interface")
}
