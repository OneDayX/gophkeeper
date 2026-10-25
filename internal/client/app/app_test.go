package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/storage"
	serverapp "github.com/OneDayX/gophkeeper/internal/server/app"
	"github.com/OneDayX/gophkeeper/internal/server/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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

// newApp returns the client of a fresh device.
func newApp(t *testing.T, server string) *App {
	t.Helper()

	a, err := New(Config{DataDir: t.TempDir(), Server: server})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	return a
}

func TestApp_TwoDevices(t *testing.T) {
	server := startServer(t)
	ctx := context.Background()

	laptop := newApp(t, server)
	assert.False(t, laptop.LoggedIn())
	assert.Empty(t, laptop.User())
	assert.Equal(t, server, laptop.Server())

	_, err := laptop.Vault()
	assert.ErrorIs(t, err, models.ErrNotLoggedIn)

	require.NoError(t, laptop.Register(ctx, "alice", "master password"))
	assert.True(t, laptop.LoggedIn())
	assert.Equal(t, "alice", laptop.User())

	v, err := laptop.Vault()
	require.NoError(t, err)
	same, err := laptop.Vault()
	require.NoError(t, err)
	assert.Same(t, v, same, "the vault is opened once")

	_, err = v.Add(models.Secret{Name: "GitHub", Payload: models.Credentials{Login: "alice", Password: "s3cret"}})
	require.NoError(t, err)
	_, err = v.Sync(ctx)
	require.NoError(t, err)

	phone := newApp(t, server)
	err = phone.Login(ctx, "alice", "wrong password")
	assert.ErrorIs(t, err, models.ErrInvalidCredentials)
	assert.False(t, phone.LoggedIn())

	require.NoError(t, phone.Login(ctx, "alice", "master password"))
	pv, err := phone.Vault()
	require.NoError(t, err)
	_, err = pv.Sync(ctx)
	require.NoError(t, err)

	entries, err := pv.List()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, models.Credentials{Login: "alice", Password: "s3cret"}, entries[0].Secret.Payload)
}

func TestApp_SessionSurvivesRestart(t *testing.T) {
	server := startServer(t)
	ctx := context.Background()
	dir := t.TempDir()
	cfg := Config{DataDir: dir, Server: server}

	first, err := New(cfg)
	require.NoError(t, err)
	require.NoError(t, first.Register(ctx, "alice", "master password"))
	v, err := first.Vault()
	require.NoError(t, err)
	_, err = v.Add(models.Secret{Name: "note", Payload: models.Text{Content: "kept locally"}})
	require.NoError(t, err)
	require.NoError(t, first.Close())

	// The next command is a new process: it finds the session and the vault.
	second, err := New(Config{DataDir: dir, Server: "elsewhere:1"})
	require.NoError(t, err)
	defer second.Close()
	assert.True(t, second.LoggedIn())
	assert.Equal(t, server, second.Server(), "the server of the session wins")

	v, err = second.Vault()
	require.NoError(t, err)
	_, err = v.Sync(ctx)
	require.NoError(t, err, "the saved token works")
	entries, err := v.List()
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	// A second process cannot open the same vault at the same time.
	third, err := New(cfg)
	require.NoError(t, err)
	_, err = third.Vault()
	assert.ErrorIs(t, err, storage.ErrLocked)

	require.NoError(t, second.Logout())
	assert.False(t, second.LoggedIn())
	_, err = second.Vault()
	assert.ErrorIs(t, err, models.ErrNotLoggedIn)

	after, err := New(cfg)
	require.NoError(t, err)
	assert.False(t, after.LoggedIn())
}

func TestApp_SwitchAccounts(t *testing.T) {
	server := startServer(t)
	ctx := context.Background()
	a := newApp(t, server)

	require.NoError(t, a.Register(ctx, "alice", "alice password"))
	v, err := a.Vault()
	require.NoError(t, err)
	_, err = v.Add(models.Secret{Name: "alice's", Payload: models.Text{Content: "x"}})
	require.NoError(t, err)

	// Bob on the same device gets a vault of his own.
	require.NoError(t, a.Register(ctx, "bob", "bob password"))
	v, err = a.Vault()
	require.NoError(t, err)
	entries, err := v.List()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestApp_Errors(t *testing.T) {
	server := startServer(t)
	ctx := context.Background()
	a := newApp(t, server)

	assert.ErrorIs(t, a.Register(ctx, "", "password"), ErrEmptyCredentials)
	assert.ErrorIs(t, a.Login(ctx, "alice", ""), ErrEmptyCredentials)
	assert.ErrorIs(t, a.Register(ctx, "alice", "short"), ErrWeakPassword)

	require.NoError(t, a.Register(ctx, "alice", "master password"))
	assert.ErrorIs(t, a.Register(ctx, "alice", "master password"), models.ErrLoginTaken)

	badCA, err := New(Config{DataDir: t.TempDir(), Server: server, CACert: filepath.Join(t.TempDir(), "missing.pem")})
	require.NoError(t, err)
	assert.Error(t, badCA.Login(ctx, "alice", "master password"))

	// A broken session file is reported, not ignored.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.json"), []byte("{broken"), 0o600))
	_, err = New(Config{DataDir: dir})
	assert.Error(t, err)

	// A data directory that is a file cannot hold a session.
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err = New(Config{DataDir: file})
	assert.Error(t, err)
}
