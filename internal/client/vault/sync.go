package vault

import (
	"context"
	"errors"
	"fmt"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/google/uuid"
)

// Sync sends the local changes to the server and applies the changes made on
// other devices since the last sync.
//
// It pulls first and pushes second. Each item carries the server version its
// local copy is based on, so a conflict is easy to see: the server has a
// newer version of an item that has also changed here. Then the server
// version wins, and the local change is kept as a new entry named
// "<name> (conflict <time>)", so no change is ever lost. A change beats a
// deletion: an entry deleted on one device and edited on another survives.
//
// If another device changes an item between the pull and the push, the
// server refuses the push, and Sync goes round again, up to maxSyncRounds
// times; what is left waits for the next sync.
func (v *Vault) Sync(ctx context.Context) (SyncResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	var result SyncResult
	for range maxSyncRounds {
		if err := v.pull(ctx, &result); err != nil {
			return result, err
		}

		raced, err := v.push(ctx, &result)
		if err != nil {
			return result, err
		}
		if !raced {
			break
		}
	}

	return result, nil
}

// pull applies the changes the server has got since the last pull, and
// moves the cursor past them.
func (v *Vault) pull(ctx context.Context, result *SyncResult) error {
	cursor, err := v.local.Cursor()
	if err != nil {
		return err
	}

	var changes []models.Item
	err = v.remote.ListItems(ctx, cursor, func(item models.Item) error {
		changes = append(changes, item)
		return nil
	})
	if err != nil {
		return fmt.Errorf("fetch changes: %w", err)
	}

	var (
		put    []models.Item
		remove []string
	)
	for _, remote := range changes {
		cursor = max(cursor, remote.Version)

		local, err := v.local.Get(remote.ID)
		found := err == nil
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			return err
		}

		if found && local.Version == remote.Version {
			// Either our own push coming back, or a local change based on
			// this very version, which the push will send.
			continue
		}
		if !found && remote.Deleted {
			// A deletion of an item this device has never had.
			continue
		}

		if found && local.Dirty && !local.Deleted {
			// The item has changed on both sides: the local change becomes
			// a copy, and the server version takes its place.
			conflict, err := v.conflictCopy(local)
			if err != nil {
				return err
			}
			put = append(put, conflict)
			result.Conflicts++
		}

		if remote.Deleted {
			remove = append(remove, remote.ID)
		} else {
			put = append(put, remote)
		}
		result.Pulled++
	}

	if len(changes) == 0 {
		return nil
	}

	return v.local.Apply(put, remove, cursor)
}

// push sends the local changes one by one. It reports raced if the server
// has refused some of them because another device has got there first; the
// next pull brings those versions.
func (v *Vault) push(ctx context.Context, result *SyncResult) (raced bool, err error) {
	items, err := v.local.List()
	if err != nil {
		return false, err
	}

	for _, item := range items {
		if !item.Dirty {
			continue
		}

		err := v.pushItem(ctx, item)
		switch {
		case errors.Is(err, models.ErrConflict), errors.Is(err, errRecreate):
			raced = true
		case err != nil:
			return raced, fmt.Errorf("send changes: %w", err)
		default:
			result.Pushed++
		}
	}

	return raced, nil
}

// errRecreate means the server has lost an item the device has a change
// for: the item is turned into a new one for the next round.
var errRecreate = errors.New("item is gone from the server, sending it again")

// pushItem sends one local change and records that the server has it.
func (v *Vault) pushItem(ctx context.Context, item models.Item) error {
	switch {
	case item.Deleted:
		_, err := v.remote.DeleteItem(ctx, item.ID, item.Version)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			return err
		}
		// The item is gone from the server either way.
		return v.local.Delete(item.ID)

	case item.Version == 0:
		created, err := v.remote.CreateItem(ctx, item)
		if err != nil {
			return err
		}
		return v.markSynced(item, created)

	default:
		updated, err := v.remote.UpdateItem(ctx, item)
		if errors.Is(err, models.ErrNotFound) {
			// Not even a tombstone is left: the server has lost the item.
			// Without a version the next round creates it again.
			item.Version = 0
			if err := v.local.Put(item); err != nil {
				return err
			}
			return errRecreate
		}
		if err != nil {
			return err
		}
		return v.markSynced(item, updated)
	}
}

// markSynced records the version and time the server has given to a pushed
// item.
func (v *Vault) markSynced(item, stored models.Item) error {
	item.Version = stored.Version
	item.UpdatedAt = stored.UpdatedAt
	item.Dirty = false

	return v.local.Put(item)
}

// conflictCopy turns a local change that has lost to a change from another
// device into a new entry, so that it is not lost.
func (v *Vault) conflictCopy(local models.Item) (models.Item, error) {
	s, err := v.unseal(local)
	if err != nil {
		return models.Item{}, err
	}
	s.Name = fmt.Sprintf("%s (conflict %s)", s.Name, v.now().Format("2006-01-02 15:04"))

	item := models.Item{
		ID:        uuid.NewString(),
		Type:      local.Type,
		Dirty:     true,
		UpdatedAt: v.now(),
	}
	if err := v.seal(&item, s); err != nil {
		return models.Item{}, err
	}

	return item, nil
}
