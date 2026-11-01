// Package storage keeps the vault of a user on the device: the items with the
// secrets still encrypted, their sync state, and the sync cursor. It uses
// bbolt, a single-file key-value store in pure Go, so the client builds for
// any platform without a C compiler.
//
// The file holds no keys: without the master password the secrets in it are
// as unreadable as on the server.
package storage

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"go.etcd.io/bbolt"
	bberrors "go.etcd.io/bbolt/errors"
)

// openTimeout is how long Open waits for another process to release the file.
const openTimeout = time.Second

var (
	// itemsBucket maps item IDs to JSON-encoded models.Item.
	itemsBucket = []byte("items")
	// metaBucket holds the sync cursor.
	metaBucket = []byte("meta")
	// cursorKey is the key of the cursor in metaBucket.
	cursorKey = []byte("cursor")
)

// ErrLocked means another process, such as the TUI, has the vault open.
var ErrLocked = errors.New("the vault is open in another gophkeeper process")

// Store is the vault of one user on the device. bbolt locks the file, so
// only one process can have it open; within the process, Store is safe for
// concurrent use.
type Store struct {
	db *bbolt.DB
}

// Open opens the vault file, creating the file and its directory if needed.
// It returns ErrLocked if another process keeps the file open.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create vault directory: %w", err)
	}

	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: openTimeout})
	if errors.Is(err, bberrors.ErrTimeout) {
		return nil, ErrLocked
	}
	if err != nil {
		return nil, fmt.Errorf("open vault: %w", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{itemsBucket, metaBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("prepare vault: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the file.
func (s *Store) Close() error {
	return s.db.Close()
}

// Get returns the item with the ID, or models.ErrNotFound.
func (s *Store) Get(id string) (models.Item, error) {
	var item models.Item

	err := s.db.View(func(tx *bbolt.Tx) error {
		data := tx.Bucket(itemsBucket).Get([]byte(id))
		if data == nil {
			return fmt.Errorf("%w: %s", models.ErrNotFound, id)
		}

		return json.Unmarshal(data, &item)
	})

	return item, err
}

// Put stores the item, replacing the one with the same ID.
func (s *Store) Put(item models.Item) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return putItem(tx, item)
	})
}

// Delete removes the item with the ID; a missing item is not an error.
func (s *Store) Delete(id string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(itemsBucket).Delete([]byte(id))
	})
}

// List returns all items, ordered by ID.
func (s *Store) List() ([]models.Item, error) {
	var items []models.Item

	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(itemsBucket).ForEach(func(_, data []byte) error {
			var item models.Item
			if err := json.Unmarshal(data, &item); err != nil {
				return err
			}
			items = append(items, item)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}

	return items, nil
}

// Cursor returns the greatest server version the vault has pulled, zero
// before the first sync.
func (s *Store) Cursor() (int64, error) {
	var cursor int64

	err := s.db.View(func(tx *bbolt.Tx) error {
		if data := tx.Bucket(metaBucket).Get(cursorKey); len(data) == 8 {
			cursor = int64(binary.BigEndian.Uint64(data))
		}
		return nil
	})

	return cursor, err
}

// Apply writes the result of a pull in one transaction: it stores the items,
// removes the ones with the listed IDs and moves the cursor. A crash leaves
// the vault either before the pull or after it, never in between.
func (s *Store) Apply(put []models.Item, remove []string, cursor int64) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		for _, item := range put {
			if err := putItem(tx, item); err != nil {
				return err
			}
		}

		items := tx.Bucket(itemsBucket)
		for _, id := range remove {
			if err := items.Delete([]byte(id)); err != nil {
				return err
			}
		}

		var data [8]byte
		binary.BigEndian.PutUint64(data[:], uint64(cursor))

		return tx.Bucket(metaBucket).Put(cursorKey, data[:])
	})
}

// putItem stores an item within a transaction.
func putItem(tx *bbolt.Tx, item models.Item) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode item: %w", err)
	}

	return tx.Bucket(itemsBucket).Put([]byte(item.ID), data)
}
