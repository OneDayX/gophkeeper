package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/OneDayX/gophkeeper/internal/client/models"
	serverapp "github.com/OneDayX/gophkeeper/internal/server/app"
	"github.com/OneDayX/gophkeeper/internal/server/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const masterPassword = "correct horse battery"

var testBuild = BuildInfo{Version: "v1.2.3", Date: "2026-10-25", Commit: "abc1234"}

// startServer runs the real server with the data in memory until the test
// ends and returns its gRPC address.
func startServer(t *testing.T) string {
	t.Helper()

	srv, err := serverapp.New(context.Background(), config.Config{
		GRPCAddress: "127.0.0.1:0",
		AuthKey:     "test-key",
		TokenTTL:    time.Hour,
	}, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		srv.Close()
	})

	return srv.GRPCAddr()
}

// device is a machine of the user: its own data directory, one server.
type device struct {
	server  string
	dataDir string
	runTUI  tuiRunner
}

func newDevice(t *testing.T, server string) *device {
	return &device{server: server, dataDir: t.TempDir()}
}

// run executes a command line on the device and returns its stdout and
// stderr.
func (d *device) run(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()

	cmd := newRootCommand(testBuild, d.runTUI)
	cmd.SetArgs(append([]string{"--server", d.server, "--insecure", "--data-dir", d.dataDir}, args...))
	cmd.SetIn(strings.NewReader(stdin))

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())

	return stdout.String(), stderr.String(), err
}

// must runs a command line that has to succeed and returns its stdout.
func (d *device) must(t *testing.T, stdin string, args ...string) string {
	t.Helper()

	stdout, stderr, err := d.run(t, stdin, args...)
	require.NoError(t, err, "gophkeeper %s\nstderr: %s", strings.Join(args, " "), stderr)

	return stdout
}

// registered returns a device with a fresh account logged in.
func registered(t *testing.T, server, login string) *device {
	t.Helper()

	d := newDevice(t, server)
	d.must(t, masterPassword+"\n"+masterPassword+"\n", "register", "--login", login)

	return d
}

func TestVersion(t *testing.T) {
	d := newDevice(t, "localhost:1")

	out := d.must(t, "", "version")
	assert.Equal(t, "GophKeeper client v1.2.3\nBuild date: 2026-10-25\nBuild commit: abc1234\n", out)

	out = d.must(t, "", "--version")
	assert.Contains(t, out, "Build date: 2026-10-25")
}

func TestRegisterAndLogin(t *testing.T) {
	server := startServer(t)

	laptop := newDevice(t, server)
	_, _, err := laptop.run(t, "", "list")
	assert.ErrorIs(t, err, models.ErrNotLoggedIn)

	_, _, err = laptop.run(t, "short\n", "register", "--login", "alice")
	assert.ErrorIs(t, err, app.ErrWeakPassword)

	_, _, err = laptop.run(t, masterPassword+"\nsomething else\n", "register", "--login", "alice")
	assert.ErrorIs(t, err, errPasswordsDiffer)

	// The login is read from the input when the flag is not set.
	out := laptop.must(t, "alice\n"+masterPassword+"\n"+masterPassword+"\n", "register")
	assert.Contains(t, out, "Registered and logged in as alice")

	_, _, err = newDevice(t, server).run(t, masterPassword+"\n"+masterPassword+"\n", "register", "-l", "alice")
	assert.ErrorIs(t, err, models.ErrLoginTaken)

	laptop.must(t, "s3cret\n", "add", "credentials", "--name", "GitHub", "--login", "alice")

	phone := newDevice(t, server)
	_, _, err = phone.run(t, "wrong password\n", "login", "--login", "alice")
	assert.ErrorIs(t, err, models.ErrInvalidCredentials)

	out = phone.must(t, masterPassword+"\n", "login", "--login", "alice")
	assert.Contains(t, out, "Logged in as alice")
	assert.Contains(t, out, "1 received")
	assert.Contains(t, phone.must(t, "", "list"), "GitHub")

	out = phone.must(t, "", "status")
	assert.Contains(t, out, "Logged in as alice at "+server)
	assert.Contains(t, out, "Entries: 1, changes not synced: 0")

	_, _, err = newDevice(t, server).run(t, "", "login", "--login", "alice")
	assert.Error(t, err, "no password to read")
	_, _, err = newDevice(t, server).run(t, "\n", "login")
	assert.ErrorIs(t, err, app.ErrEmptyCredentials)
}

func TestLogout(t *testing.T) {
	server := startServer(t)
	d := registered(t, server, "alice")

	d.must(t, "", "--offline", "add", "text", "--name", "draft", "--text", "unsent")

	stdout, stderr, err := d.run(t, "", "logout")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Logged out alice")
	assert.Contains(t, stderr, "1 change(s) are not synced yet")

	assert.Contains(t, d.must(t, "", "logout"), "Not logged in")
	assert.Contains(t, d.must(t, "", "status"), "Not logged in")

	// The draft is still on the device and goes out after the next login.
	out := d.must(t, masterPassword+"\n", "login", "-l", "alice")
	assert.Contains(t, out, "1 sent")
}

func TestEntries(t *testing.T) {
	server := startServer(t)
	d := registered(t, server, "alice")

	assert.Contains(t, d.must(t, "", "list"), "No entries")

	d.must(t, "s3cret\n", "add", "credentials", "--name", "GitHub", "--login", "alice", "--meta", "github.com")
	d.must(t, "", "add", "card", "-n", "Visa", "--number", "4111 1111 1111 1111", "--expiry", "12/30", "--cvv", "123")
	d.must(t, "line one\nline two\n", "add", "text", "--name", "Notes", "--text", "-")
	d.must(t, "", "add", "otp", "--name", "2FA", "--uri", "otpauth://totp/GitHub:alice?secret=JBSWY3DPEHPK3PXP&issuer=GitHub")

	file := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(file, []byte("private key"), 0o600))
	d.must(t, "", "add", "binary", "--name", "SSH key", "--file", file)

	list := d.must(t, "", "list")
	for _, name := range []string{"GitHub", "Visa", "Notes", "2FA", "SSH key"} {
		assert.Contains(t, list, name)
	}
	assert.NotContains(t, list, "not synced", "every change is synced at once")

	cards := d.must(t, "", "ls", "--type", "card")
	assert.Contains(t, cards, "Visa")
	assert.NotContains(t, cards, "GitHub")
	_, _, err := d.run(t, "", "list", "--type", "wallet")
	assert.ErrorIs(t, err, models.ErrUnknownType)

	// The secrets are hidden unless asked for.
	out := d.must(t, "", "show", "github")
	assert.Contains(t, out, "Login:     alice")
	assert.Contains(t, out, "********")
	assert.NotContains(t, out, "s3cret")
	assert.Contains(t, out, "github.com")
	assert.Contains(t, d.must(t, "", "show", "github", "--reveal"), "s3cret")

	out = d.must(t, "", "show", "visa")
	assert.Contains(t, out, "**** 1111")
	assert.NotContains(t, out, "4111 1111")
	assert.Contains(t, d.must(t, "", "get", "visa", "-r"), "4111 1111 1111 1111")

	assert.Contains(t, d.must(t, "", "show", "notes"), "line one\nline two")
	assert.Contains(t, d.must(t, "", "show", "2fa"), "valid for")

	code, stderr, err := d.run(t, "", "otp", "2fa")
	require.NoError(t, err)
	assert.Regexp(t, `^\d{6}\n$`, code)
	assert.Contains(t, stderr, "valid for")
	_, _, err = d.run(t, "", "otp", "visa")
	assert.Error(t, err, "not an OTP entry")

	// A binary entry is saved to a file or to stdout.
	saved := filepath.Join(t.TempDir(), "restored")
	d.must(t, "", "show", "ssh key", "--out", saved)
	data, err := os.ReadFile(saved)
	require.NoError(t, err)
	assert.Equal(t, "private key", string(data))
	assert.Equal(t, "private key", d.must(t, "", "show", "ssh key", "-o", "-"))
	_, _, err = d.run(t, "", "show", "visa", "--out", saved)
	assert.Error(t, err, "only binary entries go to files")

	_, _, err = d.run(t, "", "show", "nothing")
	assert.ErrorIs(t, err, models.ErrNotFound)
}

func TestEdit(t *testing.T) {
	server := startServer(t)
	d := registered(t, server, "alice")

	d.must(t, "old\n", "add", "credentials", "--name", "GitHub", "--login", "alice")
	d.must(t, "", "add", "card", "--name", "Visa", "--number", "4111111111111111")
	d.must(t, "", "add", "text", "--name", "Notes", "--text", "v1")
	d.must(t, "", "add", "otp", "--name", "2FA", "--secret", "JBSWY3DPEHPK3PXP")

	out := d.must(t, "new password\n", "edit", "github", "--password", "-", "--meta", "work")
	assert.Contains(t, out, "Updated GitHub")
	shown := d.must(t, "", "show", "github", "-r")
	assert.Contains(t, shown, "new password")
	assert.Contains(t, shown, "Login:     alice", "what is not given stays")

	d.must(t, "", "edit", "github", "--password", "typed", "--login", "bob", "--name", "GitHub (bob)")
	assert.Contains(t, d.must(t, "", "show", "github (bob)", "-r"), "typed")

	d.must(t, "", "edit", "visa", "--holder", "ALICE", "--expiry", "01/31", "--cvv", "321", "--number", "5555 5555 5555 4444")
	shown = d.must(t, "", "show", "visa", "-r")
	assert.Contains(t, shown, "5555 5555 5555 4444")
	assert.Contains(t, shown, "ALICE")

	d.must(t, "", "edit", "notes", "--text", "v2")
	assert.Contains(t, d.must(t, "", "show", "notes"), "v2")

	d.must(t, "", "edit", "2fa", "--issuer", "GitHub", "--account", "alice", "--digits", "8", "--period", "60", "--algorithm", "sha256")
	shown = d.must(t, "", "show", "2fa")
	assert.Contains(t, shown, "Issuer:   GitHub")
	assert.Regexp(t, `Code:\s+\d{4} \d{4}`, shown)
	d.must(t, "", "edit", "2fa", "--uri", "otpauth://totp/Other:bob?secret=GEZDGNBVGY3TQOJQ")
	assert.Contains(t, d.must(t, "", "show", "2fa"), "Issuer:   Other")
	d.must(t, "", "edit", "2fa", "--secret", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")

	file := filepath.Join(t.TempDir(), "photo.jpg")
	require.NoError(t, os.WriteFile(file, []byte("jpeg"), 0o600))
	d.must(t, "", "add", "binary", "--name", "Photo", "--file", file)
	require.NoError(t, os.WriteFile(file, []byte("jpeg v2"), 0o600))
	d.must(t, "", "edit", "photo", "--file", file)
	assert.Contains(t, d.must(t, "", "show", "photo"), "7 bytes")

	_, _, err := d.run(t, "", "edit", "visa", "--login", "x")
	assert.ErrorContains(t, err, "--login does not apply to a card entry")
	_, _, err = d.run(t, "", "edit", "visa")
	assert.ErrorContains(t, err, "nothing to change")
	_, _, err = d.run(t, "", "edit", "visa", "--cvv", "12")
	assert.ErrorIs(t, err, models.ErrInvalidSecret)
	_, _, err = d.run(t, "", "edit", "2fa", "--uri", "https://example.com")
	assert.ErrorIs(t, err, models.ErrInvalidSecret)
	_, _, err = d.run(t, "", "edit", "photo", "--file", filepath.Join(t.TempDir(), "missing"))
	assert.Error(t, err)
	_, _, err = d.run(t, "", "edit", "nothing", "--name", "x")
	assert.ErrorIs(t, err, models.ErrNotFound)
}

func TestAdd_Invalid(t *testing.T) {
	server := startServer(t)
	d := registered(t, server, "alice")

	_, _, err := d.run(t, "", "add", "card", "--name", "Visa", "--number", "4111111111111112")
	assert.ErrorIs(t, err, models.ErrInvalidSecret)

	_, _, err = d.run(t, "", "add", "text", "--text", "no name")
	assert.ErrorContains(t, err, `"name" not set`)

	_, _, err = d.run(t, "", "add", "otp", "--name", "2FA", "--uri", "otpauth://hotp/x?secret=JBSWY3DPEHPK3PXP")
	assert.ErrorIs(t, err, models.ErrInvalidSecret)

	_, _, err = d.run(t, "", "add", "binary", "--name", "missing", "--file", filepath.Join(t.TempDir(), "missing"))
	assert.Error(t, err)

	_, _, err = d.run(t, "", "add", "credentials", "--name", "no password")
	assert.Error(t, err, "the password prompt finds no input")
}

func TestDeleteAndSync(t *testing.T) {
	server := startServer(t)
	laptop := registered(t, server, "alice")
	laptop.must(t, "", "add", "text", "--name", "Notes", "--text", "v1")

	phone := newDevice(t, server)
	phone.must(t, masterPassword+"\n", "login", "--login", "alice")

	out := laptop.must(t, "", "rm", "notes")
	assert.Contains(t, out, "Deleted Notes")

	assert.Contains(t, phone.must(t, "", "sync"), "1 received")
	assert.Contains(t, phone.must(t, "", "list"), "No entries")
	assert.Contains(t, phone.must(t, "", "sync"), "up to date")

	_, _, err := laptop.run(t, "", "delete", "notes")
	assert.ErrorIs(t, err, models.ErrNotFound)
}

func TestOffline(t *testing.T) {
	server := startServer(t)
	d := registered(t, server, "alice")

	// The server moves away: the changes stay on the device.
	d.server = "127.0.0.1:1"
	session, err := os.ReadFile(filepath.Join(d.dataDir, "session.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(d.dataDir, "session.json"),
		bytes.ReplaceAll(session, []byte(server), []byte("127.0.0.1:1")), 0o600))

	stdout, stderr, err := d.run(t, "", "add", "text", "--name", "Notes", "--text", "offline")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Added Notes")
	assert.Contains(t, stderr, "not synced")

	assert.Contains(t, d.must(t, "", "list"), "not synced")

	_, _, err = d.run(t, "", "sync")
	assert.ErrorIs(t, err, models.ErrUnavailable)

	_, stderr, err = d.run(t, "", "--offline", "add", "text", "--name", "Quiet", "--text", "no warning")
	require.NoError(t, err)
	assert.Empty(t, stderr)

	assert.Contains(t, d.must(t, "", "status"), "changes not synced: 2")
}

func TestTUICommand(t *testing.T) {
	d := newDevice(t, "localhost:1")

	var got *app.App
	d.runTUI = func(_ context.Context, a *app.App) error {
		got = a
		return nil
	}

	d.must(t, "", "tui")
	require.NotNil(t, got)
	assert.False(t, got.LoggedIn())
}

func TestEnvironmentDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envServer, "keeper.example.com:443")
	t.Setenv(envDataDir, dir)
	t.Setenv(envInsecure, "true")
	t.Setenv(envCACert, "/etc/ca.pem")

	cmd := NewRootCommand(testBuild)
	flags := cmd.PersistentFlags()

	server, err := flags.GetString("server")
	require.NoError(t, err)
	assert.Equal(t, "keeper.example.com:443", server)

	dataDir, err := flags.GetString("data-dir")
	require.NoError(t, err)
	assert.Equal(t, dir, dataDir)

	insecure, err := flags.GetBool("insecure")
	require.NoError(t, err)
	assert.True(t, insecure)

	caCert, err := flags.GetString("ca-cert")
	require.NoError(t, err)
	assert.Equal(t, "/etc/ca.pem", caCert)

	assert.NotEmpty(t, defaultDataDir())
	assert.False(t, envBool("GOPHKEEPER_NOT_SET"))
}

func TestBrokenDataDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	d := &device{server: "localhost:1", dataDir: file}
	_, _, err := d.run(t, "", "status")
	assert.Error(t, err)
}

func TestServerNotRunning(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	d := newDevice(t, addr)
	_, _, err = d.run(t, masterPassword+"\n", "login", "--login", "alice")
	assert.ErrorIs(t, err, models.ErrUnavailable)
}
