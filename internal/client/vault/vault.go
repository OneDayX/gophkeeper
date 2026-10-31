// Package vault is the core of the client: it keeps the secrets of the user
// on the device, encrypted, and syncs them with the server.
//
// Every change is made locally first and marked dirty, so the client works
// offline; Sync later sends the changes to the server and fetches the ones
// made on other devices. See Sync for how it settles conflicts.
package vault

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/crypt"
	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/google/uuid"
)

type localStore interface {
	Get(id string) (models.Item, error)
	Put(item models.Item) error
	Delete(id string) error
	List() ([]models.Item, error)
	Cursor() (int64, error)
	Apply(put []models.Item, remove []string, cursor int64) error
}

type remoteStore interface {
	CreateItem(ctx context.Context, item models.Item) (models.Item, error)
	UpdateItem(ctx context.Context, item models.Item) (models.Item, error)
	DeleteItem(ctx context.Context, id string, version int64) (int64, error)
	ListItems(ctx context.Context, since int64, fn func(models.Item) error) error
}

// Entry is a decrypted item: the secret together with what the vault knows
// about it.
type Entry struct {
	// ID identifies the entry on all devices.
	ID string
	// Secret is what the user stores.
	Secret models.Secret
	// Version is the server version the entry is based on; zero if the
	// server has not seen it yet.
	Version int64
	// Pending tells that the entry has changes the server has not got yet.
	Pending bool
	// UpdatedAt is when the entry was last changed.
	UpdatedAt time.Time
}

// SyncResult tells what a sync has done.
type SyncResult struct {
	// Pulled is the number of changes from other devices applied here.
	Pulled int
	// Pushed is the number of local changes the server has accepted.
	Pushed int
	// Conflicts is the number of local changes kept as copies because
	// another device had changed the same entries.
	Conflicts int
}

// maxSyncRounds is how many times Sync pulls and pushes again when another
// device keeps changing the same entries at the same time.
const maxSyncRounds = 3

// Vault is the secrets of one user on the device. It is safe for concurrent
// use: the operations take turns, so a sync never overwrites a change made
// while it waits for the server.
type Vault struct {
	mu     sync.Mutex
	local  localStore
	remote remoteStore
	key    []byte
	now    func() time.Time
}

// New returns a vault that keeps the items in local, syncs them with remote
// and encrypts the secrets with key.
func New(local localStore, remote remoteStore, key []byte) *Vault {
	return &Vault{local: local, remote: remote, key: key, now: time.Now}
}

// Add stores a new secret and returns its entry. The secret reaches the
// server with the next sync.
func (v *Vault) Add(s models.Secret) (Entry, error) {
	if err := s.Validate(); err != nil {
		return Entry{}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	item := models.Item{
		ID:        uuid.NewString(),
		Type:      s.Type(),
		Dirty:     true,
		UpdatedAt: v.now(),
	}
	if err := v.seal(&item, s); err != nil {
		return Entry{}, err
	}

	if err := v.local.Put(item); err != nil {
		return Entry{}, fmt.Errorf("save item: %w", err)
	}

	return entryOf(item, s), nil
}

// Update replaces the secret of an entry and returns the entry. The type of
// a secret cannot change.
func (v *Vault) Update(id string, s models.Secret) (Entry, error) {
	if err := s.Validate(); err != nil {
		return Entry{}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	item, err := v.liveItem(id)
	if err != nil {
		return Entry{}, err
	}

	if s.Type() != item.Type {
		return Entry{}, fmt.Errorf("%w: a %s cannot become a %s", models.ErrInvalidSecret, item.Type, s.Type())
	}

	item.Dirty = true
	item.UpdatedAt = v.now()
	if err := v.seal(&item, s); err != nil {
		return Entry{}, err
	}

	if err := v.local.Put(item); err != nil {
		return Entry{}, fmt.Errorf("save item: %w", err)
	}

	return entryOf(item, s), nil
}

// Delete deletes an entry. An entry the server has never seen just
// disappears; the others stay as tombstones until the next sync tells the
// server.
func (v *Vault) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	item, err := v.liveItem(id)
	if err != nil {
		return err
	}

	if item.Version == 0 {
		return v.local.Delete(id)
	}

	item.Deleted = true
	item.Dirty = true
	item.Data = nil
	item.UpdatedAt = v.now()

	return v.local.Put(item)
}

// Get returns the entry with the ID, or models.ErrNotFound.
func (v *Vault) Get(id string) (Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	item, err := v.liveItem(id)
	if err != nil {
		return Entry{}, err
	}

	s, err := v.unseal(item)
	if err != nil {
		return Entry{}, err
	}

	return entryOf(item, s), nil
}

// List returns all entries, sorted by name.
func (v *Vault) List() ([]Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	items, err := v.local.List()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.Deleted {
			continue
		}

		s, err := v.unseal(item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entryOf(item, s))
	}

	slices.SortFunc(entries, func(a, b Entry) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Secret.Name), strings.ToLower(b.Secret.Name)),
			cmp.Compare(a.ID, b.ID),
		)
	})

	return entries, nil
}

// Find returns the entry the reference points at: its ID, the beginning of
// its ID, or its name in any case. It returns models.ErrNotFound if nothing
// matches and models.ErrAmbiguous if more than one entry does.
func (v *Vault) Find(ref string) (Entry, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Entry{}, fmt.Errorf("%w: empty reference", models.ErrNotFound)
	}

	entries, err := v.List()
	if err != nil {
		return Entry{}, err
	}

	var matches []Entry
	for _, e := range entries {
		if e.ID == ref {
			return e, nil
		}
		if strings.HasPrefix(e.ID, strings.ToLower(ref)) || strings.EqualFold(e.Secret.Name, ref) {
			matches = append(matches, e)
		}
	}

	switch len(matches) {
	case 0:
		return Entry{}, fmt.Errorf("%w: %q", models.ErrNotFound, ref)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, e := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", e.ID[:8], e.Secret.Name))
		}
		return Entry{}, fmt.Errorf("%w %q: %s; use a longer ID", models.ErrAmbiguous, ref, strings.Join(names, ", "))
	}
}

// Pending returns the number of local changes the server has not got yet,
// deletions included.
func (v *Vault) Pending() (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	items, err := v.local.List()
	if err != nil {
		return 0, err
	}

	pending := 0
	for _, item := range items {
		if item.Dirty {
			pending++
		}
	}

	return pending, nil
}

// liveItem returns the item with the ID unless it is missing or deleted.
func (v *Vault) liveItem(id string) (models.Item, error) {
	item, err := v.local.Get(id)
	if err != nil {
		return models.Item{}, err
	}

	if item.Deleted {
		return models.Item{}, fmt.Errorf("%w: %s", models.ErrNotFound, id)
	}

	return item, nil
}

// seal encrypts the secret into the item. The ID and the type are bound to
// the ciphertext as additional data, so the server cannot pass the data of
// one item off as another.
func (v *Vault) seal(item *models.Item, s models.Secret) error {
	plaintext, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode secret: %w", err)
	}

	item.Data, err = crypt.Encrypt(v.key, plaintext, additionalData(item.ID, item.Type))
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}

	return nil
}

// unseal decrypts the secret of the item.
func (v *Vault) unseal(item models.Item) (models.Secret, error) {
	plaintext, err := crypt.Decrypt(v.key, item.Data, additionalData(item.ID, item.Type))
	if err != nil {
		return models.Secret{}, fmt.Errorf("item %s: %w", item.ID, err)
	}

	var s models.Secret
	if err := json.Unmarshal(plaintext, &s); err != nil {
		return models.Secret{}, fmt.Errorf("decode item %s: %w", item.ID, err)
	}

	return s, nil
}

// additionalData is what the ciphertext of an item is bound to.
func additionalData(id string, t models.ItemType) []byte {
	return []byte(id + ":" + strconv.Itoa(int(t)))
}

// entryOf makes an entry out of an item and its decrypted secret.
func entryOf(item models.Item, s models.Secret) Entry {
	return Entry{
		ID:        item.ID,
		Secret:    s,
		Version:   item.Version,
		Pending:   item.Dirty,
		UpdatedAt: item.UpdatedAt,
	}
}
