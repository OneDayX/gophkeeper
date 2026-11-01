package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gophkeeper", "session.json")

	_, err := Load(path)
	assert.ErrorIs(t, err, models.ErrNotLoggedIn)

	s := Session{
		Server:   "localhost:3200",
		CACert:   "/etc/gophkeeper/ca.pem",
		Insecure: true,
		Login:    "alice",
		Token:    "token",
		Key:      []byte{1, 2, 3},
	}
	require.NoError(t, Save(path, s))

	loaded, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, s, loaded)

	// Saving again replaces the session.
	s.Token = "fresh"
	require.NoError(t, Save(path, s))
	loaded, err = Load(path)
	require.NoError(t, err)
	assert.Equal(t, "fresh", loaded.Token)

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	// No temporary files are left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	require.NoError(t, Remove(path))
	require.NoError(t, Remove(path), "removing twice is fine")
	_, err = Load(path)
	assert.ErrorIs(t, err, models.ErrNotLoggedIn)
}

func TestLoad_Broken(t *testing.T) {
	dir := t.TempDir()

	junk := filepath.Join(dir, "junk.json")
	require.NoError(t, os.WriteFile(junk, []byte("{not json"), 0o600))
	_, err := Load(junk)
	assert.Error(t, err)

	incomplete := filepath.Join(dir, "incomplete.json")
	require.NoError(t, os.WriteFile(incomplete, []byte(`{"login":"alice"}`), 0o600))
	_, err = Load(incomplete)
	assert.Error(t, err)

	_, err = Load(dir)
	assert.Error(t, err, "a directory is not a session")
}

func TestSave_Errors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	err := Save(filepath.Join(file, "session.json"), Session{Login: "alice"})
	assert.Error(t, err, "a file where the directory should be")

	dir := t.TempDir()
	err = Save(dir, Session{Login: "alice"})
	assert.Error(t, err, "a directory where the file should be")

	full := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(full, "keep"), nil, 0o600))
	assert.Error(t, Remove(full), "a directory with files is not a session to remove")
}
