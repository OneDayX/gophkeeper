package service

import (
	"context"
	"fmt"

	"github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/google/uuid"
)

type itemStore interface {
	Create(ctx context.Context, userID int64, item models.Item) (models.Item, error)
	Update(ctx context.Context, userID int64, item models.Item) (models.Item, error)
	Delete(ctx context.Context, userID int64, id string, version int64) (int64, error)
	Get(ctx context.Context, userID int64, id string) (models.Item, error)
	List(ctx context.Context, userID int64, since int64, fn func(models.Item) error) error
}

// ItemService keeps the encrypted items of the users. It checks what the
// clients send and leaves the versioning to the store. Every method works
// on the items of one user only.
type ItemService struct {
	items itemStore
}

// NewItemService returns an ItemService that keeps the items in the store.
func NewItemService(items itemStore) *ItemService {
	return &ItemService{items: items}
}

// Create stores a new item of the user and returns it with its ID, version
// and time. An empty ID gets a fresh UUID. It returns models.ErrInvalidItem
// for a malformed item and models.ErrItemExists for a taken ID.
func (s *ItemService) Create(ctx context.Context, userID int64, item models.Item) (models.Item, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}

	id, err := normalizeID(item.ID)
	if err != nil {
		return models.Item{}, err
	}
	item.ID = id

	if !item.Type.Valid() {
		return models.Item{}, fmt.Errorf("%w: unknown type %d", models.ErrInvalidItem, item.Type)
	}

	if err := checkData(item.Data); err != nil {
		return models.Item{}, err
	}

	return s.items.Create(ctx, userID, item)
}

// Update replaces the data of an item. item.Version is the version the
// client has changed; if the item has moved on since, the update fails with
// models.ErrVersionConflict. A missing or deleted item gives
// models.ErrItemNotFound.
func (s *ItemService) Update(ctx context.Context, userID int64, item models.Item) (models.Item, error) {
	id, err := normalizeID(item.ID)
	if err != nil {
		return models.Item{}, err
	}
	item.ID = id

	if err := checkVersion(item.Version); err != nil {
		return models.Item{}, err
	}

	if err := checkData(item.Data); err != nil {
		return models.Item{}, err
	}

	return s.items.Update(ctx, userID, item)
}

// Delete turns an item into a tombstone and returns its version. It fails
// like Update.
func (s *ItemService) Delete(ctx context.Context, userID int64, id string, version int64) (int64, error) {
	id, err := normalizeID(id)
	if err != nil {
		return 0, err
	}

	if err := checkVersion(version); err != nil {
		return 0, err
	}

	return s.items.Delete(ctx, userID, id, version)
}

// Get returns an item of the user. A missing or deleted item gives
// models.ErrItemNotFound.
func (s *ItemService) Get(ctx context.Context, userID int64, id string) (models.Item, error) {
	id, err := normalizeID(id)
	if err != nil {
		return models.Item{}, err
	}

	return s.items.Get(ctx, userID, id)
}

// List calls fn for every item of the user changed after the version since,
// tombstones included, in the order of versions.
func (s *ItemService) List(ctx context.Context, userID int64, since int64, fn func(models.Item) error) error {
	if since < 0 {
		return fmt.Errorf("%w: negative version %d", models.ErrInvalidItem, since)
	}

	return s.items.List(ctx, userID, since, fn)
}

// normalizeID checks that id is a UUID and returns its canonical form, so
// that one item is not stored twice under differently spelled IDs.
func normalizeID(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%w: ID %q is not a UUID", models.ErrInvalidItem, id)
	}

	return parsed.String(), nil
}

// checkVersion checks the version a client says it has changed. Versions
// start from one, so zero means the client has not set it.
func checkVersion(version int64) error {
	if version <= 0 {
		return fmt.Errorf("%w: version must be positive, got %d", models.ErrInvalidItem, version)
	}

	return nil
}

// checkData checks the size of the encrypted data. Its content is opaque.
func checkData(data []byte) error {
	switch {
	case len(data) == 0:
		return fmt.Errorf("%w: data must not be empty", models.ErrInvalidItem)
	case len(data) > models.MaxItemSize:
		return fmt.Errorf("%w: data is %d bytes, the limit is %d", models.ErrInvalidItem, len(data), models.MaxItemSize)
	default:
		return nil
	}
}
