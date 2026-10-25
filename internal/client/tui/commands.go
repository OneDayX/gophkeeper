package tui

import (
	"context"
	"os"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	tea "github.com/charmbracelet/bubbletea"
)

// The results of the operations that run in the background, so that the
// interface stays responsive while the server answers.
type (
	// authDoneMsg is the result of a login or a registration.
	authDoneMsg struct{ err error }

	// entriesMsg is the list of entries, loaded anew.
	entriesMsg struct {
		entries []vault.Entry
		err     error
	}

	// syncDoneMsg is the result of a sync.
	syncDoneMsg struct {
		result vault.SyncResult
		err    error
	}

	// savedMsg is the entry added or updated.
	savedMsg struct {
		entry vault.Entry
		err   error
	}

	// deletedMsg is the result of a deletion.
	deletedMsg struct {
		name string
		err  error
	}

	// fileSavedMsg is the result of saving a binary entry to a file.
	fileSavedMsg struct {
		path string
		err  error
	}

	// tickMsg moves the time of the OTP codes on.
	tickMsg time.Time
)

// authCmd logs in or registers. Deriving the keys takes a moment, so it runs
// in the background like the server call.
func authCmd(backend Backend, login, password string, register bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		if register {
			return authDoneMsg{err: backend.Register(ctx, login, password)}
		}

		return authDoneMsg{err: backend.Login(ctx, login, password)}
	}
}

// loadCmd reads the entries.
func loadCmd(v Vault) tea.Cmd {
	return func() tea.Msg {
		entries, err := v.List()
		return entriesMsg{entries: entries, err: err}
	}
}

// syncCmd syncs the vault with the server.
func syncCmd(v Vault) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		result, err := v.Sync(ctx)
		return syncDoneMsg{result: result, err: err}
	}
}

// saveCmd adds the secret as a new entry if id is empty, or updates the
// entry with the ID.
func saveCmd(v Vault, id string, s models.Secret) tea.Cmd {
	return func() tea.Msg {
		if id == "" {
			entry, err := v.Add(s)
			return savedMsg{entry: entry, err: err}
		}

		entry, err := v.Update(id, s)
		return savedMsg{entry: entry, err: err}
	}
}

// deleteCmd deletes the entry.
func deleteCmd(v Vault, e vault.Entry) tea.Cmd {
	return func() tea.Msg {
		return deletedMsg{name: e.Secret.Name, err: v.Delete(e.ID)}
	}
}

// writeFileCmd saves data to a file only its owner can read.
func writeFileCmd(path string, data []byte) tea.Cmd {
	return func() tea.Msg {
		return fileSavedMsg{path: path, err: os.WriteFile(path, data, 0o600)}
	}
}

// tickCmd asks for a tick in a second.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
