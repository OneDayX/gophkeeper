// Package session keeps what the client remembers between runs after a
// login: the server, the user, the token and the encryption key. Every CLI
// command is a separate process, so the session lives in a file that only
// the owner of the account can read; logout removes it.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/OneDayX/gophkeeper/internal/client/models"
)

// Session is what the client remembers after a login.
type Session struct {
	// Server is the host:port of the gRPC server.
	Server string `json:"server"`
	// CACert is the PEM file of the CA the server certificate is checked
	// against; empty means the system roots.
	CACert string `json:"ca_cert,omitempty"`
	// Insecure turns TLS off, for a development server on localhost.
	Insecure bool `json:"insecure,omitempty"`
	// Login is the login of the user.
	Login string `json:"login"`
	// Token authenticates the calls to the server.
	Token string `json:"token"`
	// Key is the encryption key derived from the master password. Keeping it
	// lets the commands work without asking for the password every time.
	Key []byte `json:"key"`
}

// Load reads the session from the file. It returns models.ErrNotLoggedIn if
// there is no session.
func Load(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Session{}, models.ErrNotLoggedIn
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("decode session %s: %w", path, err)
	}
	if s.Login == "" || s.Token == "" || len(s.Key) == 0 {
		return Session{}, fmt.Errorf("session %s is incomplete, log in again", path)
	}

	return s, nil
}

// Save writes the session to the file, readable and writable by the owner
// only. The file is replaced at once, so a crash never leaves half a session.
func Save(path string, s Session) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}

	// CreateTemp makes the file with 0600 permissions.
	tmp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return fmt.Errorf("create session file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op after the rename

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write session: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return nil
}

// Remove deletes the session file; a missing file is not an error.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove session: %w", err)
	}

	return nil
}
