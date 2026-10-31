package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "vaults", "alice.db")
	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	return s, path
}

func TestStore_Items(t *testing.T) {
	s, _ := openTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	items, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, items)

	a := models.Item{ID: "a", Type: models.ItemTypeCard, Data: []byte{1, 2}, Version: 3, UpdatedAt: now}
	b := models.Item{ID: "b", Type: models.ItemTypeText, Data: []byte{3}, Dirty: true, UpdatedAt: now}
	require.NoError(t, s.Put(b))
	require.NoError(t, s.Put(a))

	got, err := s.Get("a")
	require.NoError(t, err)
	assert.Equal(t, a, got)

	items, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, []models.Item{a, b}, items, "ordered by ID")

	a.Deleted, a.Dirty = true, true
	require.NoError(t, s.Put(a))
	got, err = s.Get("a")
	require.NoError(t, err)
	assert.True(t, got.Deleted)

	require.NoError(t, s.Delete("a"))
	require.NoError(t, s.Delete("missing"))
	_, err = s.Get("a")
	assert.ErrorIs(t, err, models.ErrNotFound)
}

func TestStore_Apply(t *testing.T) {
	s, path := openTestStore(t)

	cursor, err := s.Cursor()
	require.NoError(t, err)
	assert.Zero(t, cursor)

	require.NoError(t, s.Put(models.Item{ID: "old", Type: models.ItemTypeText}))
	require.NoError(t, s.Apply(
		[]models.Item{{ID: "new", Type: models.ItemTypeText, Version: 7}},
		[]string{"old"},
		7,
	))

	items, err := s.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "new", items[0].ID)

	cursor, err = s.Cursor()
	require.NoError(t, err)
	assert.Equal(t, int64(7), cursor)

	// Everything survives a reopen.
	require.NoError(t, s.Close())
	reopened, err := Open(path)
	require.NoError(t, err)
	defer reopened.Close()

	cursor, err = reopened.Cursor()
	require.NoError(t, err)
	assert.Equal(t, int64(7), cursor)
	_, err = reopened.Get("new")
	assert.NoError(t, err)
}

func TestOpen_Errors(t *testing.T) {
	_, path := openTestStore(t)

	// The first store still holds the lock.
	_, err := Open(path)
	assert.ErrorIs(t, err, ErrLocked)

	// A file where the directory should be.
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err = Open(filepath.Join(file, "vault.db"))
	assert.Error(t, err)

	if runtime.GOOS != "windows" {
		// Not a bbolt file.
		junk := filepath.Join(t.TempDir(), "junk.db")
		require.NoError(t, os.WriteFile(junk, []byte("definitely not a database file, just text"), 0o600))
		_, err = Open(junk)
		assert.Error(t, err)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions only")
	}

	_, path := openTestStore(t)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
}
