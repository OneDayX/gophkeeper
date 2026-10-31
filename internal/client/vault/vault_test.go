package vault

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/storage"
	smodels "github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/OneDayX/gophkeeper/internal/server/repository"
	"github.com/OneDayX/gophkeeper/internal/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const userID = 1

// fakeServer plays the server for the vaults: the real item service with a
// memory repository, without the network in between.
type fakeServer struct {
	items   *service.ItemService
	offline bool
	// beforeUpdate, if set, runs before an update reaches the service, to
	// let another device get there first.
	beforeUpdate func()
}

func newFakeServer() *fakeServer {
	return &fakeServer{items: service.NewItemService(repository.NewMemoryItemRepository())}
}

func (s *fakeServer) CreateItem(ctx context.Context, item models.Item) (models.Item, error) {
	if s.offline {
		return models.Item{}, models.ErrUnavailable
	}

	created, err := s.items.Create(ctx, userID, smodels.Item{ID: item.ID, Type: smodels.ItemType(item.Type), Data: item.Data})
	if err != nil {
		return models.Item{}, clientError(err)
	}

	item.Version, item.UpdatedAt = created.Version, created.UpdatedAt
	return item, nil
}

func (s *fakeServer) UpdateItem(ctx context.Context, item models.Item) (models.Item, error) {
	if s.beforeUpdate != nil {
		hook := s.beforeUpdate
		s.beforeUpdate = nil
		hook()
	}
	if s.offline {
		return models.Item{}, models.ErrUnavailable
	}

	updated, err := s.items.Update(ctx, userID, smodels.Item{ID: item.ID, Data: item.Data, Version: item.Version})
	if err != nil {
		return models.Item{}, clientError(err)
	}

	item.Version, item.UpdatedAt = updated.Version, updated.UpdatedAt
	return item, nil
}

func (s *fakeServer) DeleteItem(ctx context.Context, id string, version int64) (int64, error) {
	if s.offline {
		return 0, models.ErrUnavailable
	}

	deleted, err := s.items.Delete(ctx, userID, id, version)
	return deleted, clientError(err)
}

func (s *fakeServer) ListItems(ctx context.Context, since int64, fn func(models.Item) error) error {
	if s.offline {
		return models.ErrUnavailable
	}

	return s.items.List(ctx, userID, since, func(item smodels.Item) error {
		return fn(models.Item{
			ID:        item.ID,
			Type:      models.ItemType(item.Type),
			Data:      item.Data,
			Version:   item.Version,
			Deleted:   item.Deleted,
			UpdatedAt: item.UpdatedAt,
		})
	})
}

// clientError turns a server error into what the remote client returns.
func clientError(err error) error {
	switch {
	case errors.Is(err, smodels.ErrItemExists), errors.Is(err, smodels.ErrVersionConflict):
		return models.ErrConflict
	case errors.Is(err, smodels.ErrItemNotFound):
		return models.ErrNotFound
	default:
		return err
	}
}

// testKey is the encryption key of the test user on every device.
var testKey = bytes.Repeat([]byte{42}, 32)

// newDevice returns a vault on a fresh device of the user.
func newDevice(t *testing.T, server remoteStore) *Vault {
	t.Helper()

	store, err := storage.Open(filepath.Join(t.TempDir(), "vault.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	return New(store, server, testKey)
}

// mustSync syncs the vault and returns what it did.
func mustSync(t *testing.T, v *Vault) SyncResult {
	t.Helper()

	result, err := v.Sync(context.Background())
	require.NoError(t, err)

	return result
}

// names returns the names of the entries in the vault.
func names(t *testing.T, v *Vault) []string {
	t.Helper()

	entries, err := v.List()
	require.NoError(t, err)

	var names []string
	for _, e := range entries {
		names = append(names, e.Secret.Name)
	}

	return names
}

func card(name string) models.Secret {
	return models.Secret{Name: name, Metadata: "bank", Payload: models.Card{Number: "4111111111111111", CVV: "123"}}
}

func note(name, text string) models.Secret {
	return models.Secret{Name: name, Payload: models.Text{Content: text}}
}

func TestVault_LocalOperations(t *testing.T) {
	v := newDevice(t, newFakeServer())

	visa, err := v.Add(card("Visa"))
	require.NoError(t, err)
	assert.True(t, visa.Pending)
	assert.Zero(t, visa.Version)

	_, err = v.Add(note("diary", "dear diary"))
	require.NoError(t, err)

	_, err = v.Add(models.Secret{Name: "", Payload: models.Text{Content: "x"}})
	assert.ErrorIs(t, err, models.ErrInvalidSecret)

	got, err := v.Get(visa.ID)
	require.NoError(t, err)
	assert.Equal(t, card("Visa"), got.Secret)

	assert.Equal(t, []string{"diary", "Visa"}, names(t, v), "sorted by name, case aside")

	updated, err := v.Update(visa.ID, card("Visa Gold"))
	require.NoError(t, err)
	assert.Equal(t, "Visa Gold", updated.Secret.Name)

	_, err = v.Update(visa.ID, note("Visa", "a card cannot turn into a note"))
	assert.ErrorIs(t, err, models.ErrInvalidSecret)
	_, err = v.Update(visa.ID, models.Secret{Name: "no payload"})
	assert.ErrorIs(t, err, models.ErrInvalidSecret)
	_, err = v.Update("missing", card("x"))
	assert.ErrorIs(t, err, models.ErrNotFound)

	pending, err := v.Pending()
	require.NoError(t, err)
	assert.Equal(t, 2, pending)

	// An entry the server has never seen just disappears.
	require.NoError(t, v.Delete(visa.ID))
	_, err = v.Get(visa.ID)
	assert.ErrorIs(t, err, models.ErrNotFound)
	assert.ErrorIs(t, v.Delete(visa.ID), models.ErrNotFound)

	pending, err = v.Pending()
	require.NoError(t, err)
	assert.Equal(t, 1, pending)
}

func TestVault_Find(t *testing.T) {
	v := newDevice(t, newFakeServer())

	github, err := v.Add(note("GitHub", "x"))
	require.NoError(t, err)
	_, err = v.Add(note("gitlab", "x"))
	require.NoError(t, err)
	_, err = v.Add(note("twin", "x"))
	require.NoError(t, err)
	_, err = v.Add(note("TWIN", "x"))
	require.NoError(t, err)

	for _, ref := range []string{github.ID, github.ID[:8], strings.ToUpper(github.ID[:8]), "github", " GitHub "} {
		found, err := v.Find(ref)
		require.NoError(t, err, ref)
		assert.Equal(t, github.ID, found.ID, ref)
	}

	_, err = v.Find("twin")
	assert.ErrorIs(t, err, models.ErrAmbiguous)
	assert.Contains(t, err.Error(), "TWIN")

	_, err = v.Find("nothing")
	assert.ErrorIs(t, err, models.ErrNotFound)
	_, err = v.Find("  ")
	assert.ErrorIs(t, err, models.ErrNotFound)
}

func TestSync_BetweenDevices(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)
	phone := newDevice(t, server)

	visa, err := laptop.Add(card("Visa"))
	require.NoError(t, err)
	_, err = laptop.Add(note("diary", "dear diary"))
	require.NoError(t, err)

	assert.Equal(t, SyncResult{Pushed: 2}, mustSync(t, laptop))
	synced, err := laptop.Get(visa.ID)
	require.NoError(t, err)
	assert.False(t, synced.Pending)
	assert.Positive(t, synced.Version)

	// A sync with nothing new does nothing, even though the server sends
	// back the laptop's own changes once.
	assert.Equal(t, SyncResult{}, mustSync(t, laptop))
	assert.Equal(t, SyncResult{}, mustSync(t, laptop))

	assert.Equal(t, SyncResult{Pulled: 2}, mustSync(t, phone))
	got, err := phone.Get(visa.ID)
	require.NoError(t, err)
	assert.Equal(t, card("Visa"), got.Secret)

	// An edit on the phone reaches the laptop.
	_, err = phone.Update(visa.ID, card("Visa Gold"))
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Pushed: 1}, mustSync(t, phone))
	assert.Equal(t, SyncResult{Pulled: 1}, mustSync(t, laptop))
	assert.Equal(t, []string{"diary", "Visa Gold"}, names(t, laptop))

	// So does a deletion.
	require.NoError(t, laptop.Delete(visa.ID))
	pending, err := laptop.Pending()
	require.NoError(t, err)
	assert.Equal(t, 1, pending, "the deletion waits for the sync")
	assert.Equal(t, SyncResult{Pushed: 1}, mustSync(t, laptop))
	assert.Equal(t, SyncResult{Pulled: 1}, mustSync(t, phone))
	assert.Equal(t, []string{"diary"}, names(t, phone))

	// A new device gets only what is alive.
	tablet := newDevice(t, server)
	assert.Equal(t, SyncResult{Pulled: 1}, mustSync(t, tablet))
	assert.Equal(t, []string{"diary"}, names(t, tablet))
}

func TestSync_ConflictKeepsBothVersions(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)
	phone := newDevice(t, server)

	diary, err := laptop.Add(note("diary", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)
	mustSync(t, phone)

	_, err = laptop.Update(diary.ID, note("diary", "laptop edit"))
	require.NoError(t, err)
	_, err = phone.Update(diary.ID, note("diary", "phone edit"))
	require.NoError(t, err)

	mustSync(t, laptop)
	result := mustSync(t, phone)
	assert.Equal(t, 1, result.Conflicts)

	// The phone has the laptop version under the old ID and its own edit as a copy.
	got, err := phone.Get(diary.ID)
	require.NoError(t, err)
	assert.Equal(t, models.Text{Content: "laptop edit"}, got.Secret.Payload)

	entries, err := phone.List()
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.True(t, strings.HasPrefix(entries[1].Secret.Name, "diary (conflict "))
	assert.Equal(t, models.Text{Content: "phone edit"}, entries[1].Secret.Payload)

	// The copy reaches the laptop too.
	mustSync(t, laptop)
	assert.Len(t, names(t, laptop), 2)
}

func TestSync_EditBeatsDelete(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)
	phone := newDevice(t, server)

	diary, err := laptop.Add(note("diary", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)
	mustSync(t, phone)

	// The laptop deletes, the phone edits: the edit survives as a copy.
	require.NoError(t, laptop.Delete(diary.ID))
	mustSync(t, laptop)
	_, err = phone.Update(diary.ID, note("diary", "still needed"))
	require.NoError(t, err)
	result := mustSync(t, phone)
	assert.Equal(t, 1, result.Conflicts)
	_, err = phone.Get(diary.ID)
	assert.ErrorIs(t, err, models.ErrNotFound)
	require.Len(t, names(t, phone), 1)

	// The other way round: the laptop edits, the phone deletes. The deletion
	// is refused by the server and dropped, and the edit stays.
	edited, err := laptop.Add(note("todo", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)
	mustSync(t, phone)
	_, err = laptop.Update(edited.ID, note("todo", "v2"))
	require.NoError(t, err)
	mustSync(t, laptop)
	require.NoError(t, phone.Delete(edited.ID))
	mustSync(t, phone)

	got, err := phone.Get(edited.ID)
	require.NoError(t, err)
	assert.Equal(t, models.Text{Content: "v2"}, got.Secret.Payload)
}

func TestSync_RaceBetweenPullAndPush(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)
	phone := newDevice(t, server)

	diary, err := laptop.Add(note("diary", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)
	mustSync(t, phone)

	_, err = phone.Update(diary.ID, note("diary", "phone edit"))
	require.NoError(t, err)
	_, err = laptop.Update(diary.ID, note("diary", "laptop edit"))
	require.NoError(t, err)

	// The phone syncs right after the laptop has pulled but before it
	// pushes, so the server refuses the laptop's update.
	server.beforeUpdate = func() { mustSync(t, phone) }
	result := mustSync(t, laptop)
	assert.Equal(t, 1, result.Conflicts, "the second round settles it")

	pending, err := laptop.Pending()
	require.NoError(t, err)
	assert.Zero(t, pending)
	assert.Len(t, names(t, laptop), 2)
}

func TestSync_Offline(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)

	server.offline = true
	_, err := laptop.Add(note("diary", "written offline"))
	require.NoError(t, err)

	_, err = laptop.Sync(context.Background())
	assert.ErrorIs(t, err, models.ErrUnavailable)

	pending, err := laptop.Pending()
	require.NoError(t, err)
	assert.Equal(t, 1, pending, "the change waits")

	server.offline = false
	assert.Equal(t, SyncResult{Pushed: 1}, mustSync(t, laptop))
}

func TestSync_ServerLostItem(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)

	diary, err := laptop.Add(note("diary", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)

	// The server starts over from an empty database, cursor and all.
	*server = *newFakeServer()
	_, err = laptop.Update(diary.ID, note("diary", "v2"))
	require.NoError(t, err)
	mustSync(t, laptop)

	fresh := newDevice(t, server)
	mustSync(t, fresh)
	got, err := fresh.Get(diary.ID)
	require.NoError(t, err)
	assert.Equal(t, models.Text{Content: "v2"}, got.Secret.Payload)
}

func TestSync_PushErrors(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)

	diary, err := laptop.Add(note("diary", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)

	// The server goes away between the pull and the push.
	server.beforeUpdate = func() { server.offline = true }
	_, err = laptop.Update(diary.ID, note("diary", "v2"))
	require.NoError(t, err)
	_, err = laptop.Sync(context.Background())
	assert.ErrorIs(t, err, models.ErrUnavailable)
}

func TestVault_WrongKey(t *testing.T) {
	server := newFakeServer()
	laptop := newDevice(t, server)
	_, err := laptop.Add(note("diary", "v1"))
	require.NoError(t, err)
	mustSync(t, laptop)

	store, err := storage.Open(filepath.Join(t.TempDir(), "vault.db"))
	require.NoError(t, err)
	defer store.Close()
	intruder := New(store, server, bytes.Repeat([]byte{1}, 32))
	mustSync(t, intruder)

	_, err = intruder.List()
	assert.Error(t, err, "the data is of no use without the key")
}

func TestVault_UsesClock(t *testing.T) {
	v := newDevice(t, newFakeServer())
	moment := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	v.now = func() time.Time { return moment }

	e, err := v.Add(note("diary", "x"))
	require.NoError(t, err)
	assert.Equal(t, moment, e.UpdatedAt)
}
