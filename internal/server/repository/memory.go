package repository

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/OneDayX/gophkeeper/internal/server/models"
)

// MemoryUserRepository keeps users in memory, by the same rules as
// UserRepository. Everything is lost on restart. It is safe for concurrent
// use.
type MemoryUserRepository struct {
	mu      sync.Mutex
	byLogin map[string]models.User
	lastID  int64
}

// NewMemoryUserRepository returns an empty MemoryUserRepository.
func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{byLogin: make(map[string]models.User)}
}

// Create stores a new user and returns its ID. It returns
// models.ErrLoginTaken if another user has the login.
func (r *MemoryUserRepository) Create(_ context.Context, login, passwordHash string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.byLogin[login]; ok {
		return 0, fmt.Errorf("%w: %q", models.ErrLoginTaken, login)
	}

	r.lastID++
	r.byLogin[login] = models.User{ID: r.lastID, Login: login, PasswordHash: passwordHash}

	return r.lastID, nil
}

// GetByLogin returns the user with this login, or models.ErrUserNotFound.
func (r *MemoryUserRepository) GetByLogin(_ context.Context, login string) (models.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	user, ok := r.byLogin[login]
	if !ok {
		return models.User{}, fmt.Errorf("%w: %q", models.ErrUserNotFound, login)
	}

	return user, nil
}

// MemoryItemRepository keeps items in memory, by the same rules as
// ItemRepository: every write gives the item the next version of its owner.
// Everything is lost on restart. It is safe for concurrent use.
type MemoryItemRepository struct {
	mu sync.Mutex
	// items maps a user ID to the items of the user by their IDs.
	items map[int64]map[string]models.Item
	// revisions maps a user ID to the latest version given to their items.
	revisions map[int64]int64
}

// NewMemoryItemRepository returns an empty MemoryItemRepository.
func NewMemoryItemRepository() *MemoryItemRepository {
	return &MemoryItemRepository{
		items:     make(map[int64]map[string]models.Item),
		revisions: make(map[int64]int64),
	}
}

// Create stores a new item of the user and returns it with its version and
// time. It returns models.ErrItemExists if the user already has an item with
// this ID, deleted or not.
func (r *MemoryItemRepository) Create(_ context.Context, userID int64, item models.Item) (models.Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	userItems := r.items[userID]
	if userItems == nil {
		userItems = make(map[string]models.Item)
		r.items[userID] = userItems
	}

	if _, ok := userItems[item.ID]; ok {
		return models.Item{}, fmt.Errorf("create item: %w: %s", models.ErrItemExists, item.ID)
	}

	item.Data = bytes.Clone(item.Data)
	item.Deleted = false
	item.Version = r.nextVersion(userID)
	item.UpdatedAt = time.Now()
	userItems[item.ID] = item

	return item, nil
}

// Update replaces the data of an item if item.Version is still its current
// version, and returns the item with the new version and time. It fails like
// ItemRepository.Update.
func (r *MemoryItemRepository) Update(_ context.Context, userID int64, item models.Item) (models.Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, err := r.current(userID, item.ID, item.Version)
	if err != nil {
		return models.Item{}, fmt.Errorf("update item: %w", err)
	}

	stored.Data = bytes.Clone(item.Data)
	stored.Version = r.nextVersion(userID)
	stored.UpdatedAt = time.Now()
	r.items[userID][item.ID] = stored

	item.Type = stored.Type
	item.Version = stored.Version
	item.UpdatedAt = stored.UpdatedAt

	return item, nil
}

// Delete turns an item into a tombstone if version is still its current
// version, and returns the version of the tombstone. It fails like Update.
func (r *MemoryItemRepository) Delete(_ context.Context, userID int64, id string, version int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, err := r.current(userID, id, version)
	if err != nil {
		return 0, fmt.Errorf("delete item: %w", err)
	}

	stored.Data = nil
	stored.Deleted = true
	stored.Version = r.nextVersion(userID)
	stored.UpdatedAt = time.Now()
	r.items[userID][id] = stored

	return stored.Version, nil
}

// Get returns an item of the user, or models.ErrItemNotFound if there is no
// such item or it has been deleted.
func (r *MemoryItemRepository) Get(_ context.Context, userID int64, id string) (models.Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	item, ok := r.items[userID][id]
	if !ok || item.Deleted {
		return models.Item{}, fmt.Errorf("%w: %s", models.ErrItemNotFound, id)
	}

	item.Data = bytes.Clone(item.Data)

	return item, nil
}

// List calls fn for every item of the user with a version greater than
// since, tombstones included, in the order of versions. It stops at the
// first error of fn and returns it.
func (r *MemoryItemRepository) List(ctx context.Context, userID int64, since int64, fn func(models.Item) error) error {
	// The items are copied out, so that fn runs without the lock: it may be
	// slow, sending the items over the network.
	r.mu.Lock()
	var changed []models.Item
	for _, item := range r.items[userID] {
		if item.Version > since {
			item.Data = bytes.Clone(item.Data)
			changed = append(changed, item)
		}
	}
	r.mu.Unlock()

	slices.SortFunc(changed, func(a, b models.Item) int {
		return cmp.Compare(a.Version, b.Version)
	})

	for _, item := range changed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(item); err != nil {
			return err
		}
	}

	return nil
}

// nextVersion bumps the revision of the user and returns it. The caller
// holds the lock.
func (r *MemoryItemRepository) nextVersion(userID int64) int64 {
	r.revisions[userID]++
	return r.revisions[userID]
}

// current returns the live item with the given version, or the reason it
// cannot be changed. The caller holds the lock.
func (r *MemoryItemRepository) current(userID int64, id string, version int64) (models.Item, error) {
	stored, ok := r.items[userID][id]

	switch {
	case !ok:
		return models.Item{}, fmt.Errorf("%w: %s", models.ErrItemNotFound, id)
	case stored.Version != version:
		return models.Item{}, fmt.Errorf("%w: %s is at version %d, not %d", models.ErrVersionConflict, id, stored.Version, version)
	case stored.Deleted:
		return models.Item{}, fmt.Errorf("%w: %s is deleted", models.ErrItemNotFound, id)
	default:
		return stored, nil
	}
}
