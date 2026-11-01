// Package app ties the client together for the CLI and the TUI. It keeps the
// session, derives the keys from the master password at login, and opens
// the vault of the user with its local storage and the connection to the
// server.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/OneDayX/gophkeeper/internal/client/crypt"
	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/remote"
	"github.com/OneDayX/gophkeeper/internal/client/session"
	"github.com/OneDayX/gophkeeper/internal/client/storage"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
)

// MinPasswordLength is the shortest master password Register accepts. The
// server never sees the password, so only the client can insist on it.
const MinPasswordLength = 8

var (
	// ErrEmptyCredentials means the login or the password is empty.
	ErrEmptyCredentials = errors.New("login and password must not be empty")

	// ErrWeakPassword means the master password of a new account is shorter
	// than MinPasswordLength.
	ErrWeakPassword = fmt.Errorf("the master password must be at least %d characters long", MinPasswordLength)
)

// Config tells the client where its files are and how to reach the server.
type Config struct {
	// DataDir holds the session file and the vaults.
	DataDir string
	// Server is the host:port of the server to register and log in at. The
	// other commands use the server of the session.
	Server string
	// CACert is the PEM file of the CA of the server certificate; empty
	// means the CAs the system trusts.
	CACert string
	// Insecure turns TLS off for a server on another machine too; a server
	// on this machine is reached without TLS anyway.
	Insecure bool
}

// App is the client on one device. It is not safe for concurrent use; the
// vault it opens is.
type App struct {
	cfg Config

	// sess is nil when nobody is logged in.
	sess *session.Session

	// These are opened on demand and closed by Close.
	remote *remote.Client
	store  *storage.Store
	vault  *vault.Vault
}

// New loads the session, if there is one. It neither connects to the server
// nor opens the vault yet.
func New(cfg Config) (*App, error) {
	a := &App{cfg: cfg}

	s, err := session.Load(a.sessionPath())
	switch {
	case errors.Is(err, models.ErrNotLoggedIn):
		// Nobody is logged in on this device.
	case err != nil:
		return nil, err
	default:
		a.sess = &s
	}

	return a, nil
}

// LoggedIn reports whether a user is logged in on this device.
func (a *App) LoggedIn() bool {
	return a.sess != nil
}

// User returns the login of the logged-in user, or an empty string.
func (a *App) User() string {
	if a.sess == nil {
		return ""
	}

	return a.sess.Login
}

// Server returns the server of the session, or the configured one if nobody
// is logged in.
func (a *App) Server() string {
	if a.sess == nil {
		return a.cfg.Server
	}

	return a.sess.Server
}

// Register creates an account on the server and logs in to it. The master
// password must be at least MinPasswordLength characters long.
func (a *App) Register(ctx context.Context, login, password string) error {
	if password != "" && len([]rune(password)) < MinPasswordLength {
		return ErrWeakPassword
	}

	return a.authenticate(ctx, login, password, true)
}

// Login logs in to an existing account and remembers the session. The vault
// already on the device, if any, is kept: the next sync brings it up to date.
func (a *App) Login(ctx context.Context, login, password string) error {
	return a.authenticate(ctx, login, password, false)
}

// authenticate derives the keys, gets a token for them and saves the session.
func (a *App) authenticate(ctx context.Context, login, password string, register bool) error {
	if login == "" || password == "" {
		return ErrEmptyCredentials
	}

	// The server gets the auth key, never the password itself.
	keys := crypt.DeriveKeys(login, password)

	client, err := remote.New(remote.Options{Address: a.cfg.Server, CACert: a.cfg.CACert, Insecure: a.cfg.Insecure})
	if err != nil {
		return err
	}

	var token string
	if register {
		token, err = client.Register(ctx, login, keys.Auth)
	} else {
		token, err = client.Login(ctx, login, keys.Auth)
	}
	if err != nil {
		_ = client.Close()
		return err
	}
	client.SetToken(token)

	s := session.Session{
		Server:   a.cfg.Server,
		CACert:   a.cfg.CACert,
		Insecure: a.cfg.Insecure,
		Login:    login,
		Token:    token,
		Key:      keys.Encryption,
	}
	if err := session.Save(a.sessionPath(), s); err != nil {
		_ = client.Close()
		return err
	}

	// The vault of a previous session, if open, belongs to another login.
	if err := a.Close(); err != nil {
		_ = client.Close()
		return err
	}
	a.sess = &s
	a.remote = client

	return nil
}

// Logout forgets the session: the token and the key leave the device. The
// vault file stays for the next login; without the master password its
// secrets cannot be read.
func (a *App) Logout() error {
	if err := a.Close(); err != nil {
		return err
	}
	a.sess = nil

	return session.Remove(a.sessionPath())
}

// Vault opens the vault of the logged-in user, or returns the one already
// open. It returns models.ErrNotLoggedIn if nobody is logged in.
func (a *App) Vault() (*vault.Vault, error) {
	if a.vault != nil {
		return a.vault, nil
	}
	if a.sess == nil {
		return nil, models.ErrNotLoggedIn
	}

	store, err := storage.Open(a.vaultPath())
	if err != nil {
		return nil, err
	}

	if a.remote == nil {
		client, err := remote.New(remote.Options{Address: a.sess.Server, CACert: a.sess.CACert, Insecure: a.sess.Insecure})
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		client.SetToken(a.sess.Token)
		a.remote = client
	}

	a.store = store
	a.vault = vault.New(store, a.remote, a.sess.Key)

	return a.vault, nil
}

// Close releases the vault file and the connection to the server. The App
// can open them again.
func (a *App) Close() error {
	var errs []error

	if a.store != nil {
		errs = append(errs, a.store.Close())
		a.store = nil
	}
	if a.remote != nil {
		errs = append(errs, a.remote.Close())
		a.remote = nil
	}
	a.vault = nil

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("close vault: %w", err)
	}

	return nil
}

// sessionPath returns the file of the session.
func (a *App) sessionPath() string {
	return filepath.Join(a.cfg.DataDir, "session.json")
}

// vaultPath returns the vault file of the logged-in user. Every login at
// every server has a file of its own, so switching accounts never mixes
// their secrets.
func (a *App) vaultPath() string {
	sum := sha256.Sum256([]byte(a.sess.Server + "\n" + a.sess.Login))
	return filepath.Join(a.cfg.DataDir, "vaults", hex.EncodeToString(sum[:8])+".db")
}
