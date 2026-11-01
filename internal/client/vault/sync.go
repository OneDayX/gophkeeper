package vault

import (
	"bytes"
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
	v.syncMu.Lock()
	defer v.syncMu.Unlock()

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

// pull applies the changes one at a time as they arrive and moves the cursor
// with each item. Neither the stream nor its changes are buffered in memory.
func (v *Vault) pull(ctx context.Context, result *SyncResult) error {
	v.mu.Lock()
	cursor, err := v.local.Cursor()
	v.mu.Unlock()
	if err != nil {
		return err
	}

	for remote, err := range v.remote.ListItems(ctx, cursor) {
		if err != nil {
			return fmt.Errorf("fetch changes: %w", err)
		}
		cursor, err = v.applyRemote(remote, cursor, result)
		if err != nil {
			return err
		}
	}
	return nil
}

// applyRemote applies one server item and its cursor in the same transaction.
// The lock covers only this local work, not the network stream.
func (v *Vault) applyRemote(remote models.Item, cursor int64, result *SyncResult) (int64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	cursor = max(cursor, remote.Version)
	local, err := v.local.Get(remote.ID)
	found := err == nil
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return cursor, err
	}

	var put []models.Item
	var remove []string
	switch {
	case found && local.Version == remote.Version:
		// Our own push, or a local change based on this very version.
	case !found && remote.Deleted:
		// A deletion of an item this device has never had.
	default:
		if found && local.Dirty && !local.Deleted {
			conflict, err := v.conflictCopy(local)
			if err != nil {
				return cursor, err
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

	return cursor, v.local.Apply(put, remove, cursor)
}

// push sends the local changes one by one. It reports raced if the server
// has refused some of them because another device has got there first; the
// next pull brings those versions.
func (v *Vault) push(ctx context.Context, result *SyncResult) (raced bool, err error) {
	v.mu.Lock()
	items, err := v.local.List()
	v.mu.Unlock()
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

// pushItem sends one local change and records that the server has it. The
// remote call runs without the local lock.
func (v *Vault) pushItem(ctx context.Context, item models.Item) error {
	switch {
	case item.Deleted:
		_, err := v.remote.DeleteItem(ctx, item.ID, item.Version)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			return err
		}
		v.mu.Lock()
		defer v.mu.Unlock()
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
			return v.markForRecreate(item)
		}
		if err != nil {
			return err
		}
		return v.markSynced(item, updated)
	}
}

// markSynced records the answer to a push. If the user changed the item while
// it was being sent, only its base version moves: the newer edit stays dirty.
func (v *Vault) markSynced(item, stored models.Item) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	current, err := v.local.Get(item.ID)
	if errors.Is(err, models.ErrNotFound) && item.Version == 0 {
		// A new item was deleted locally while its create was in flight.
		item.Version = stored.Version
		item.UpdatedAt = stored.UpdatedAt
		item.Deleted = true
		item.Data = nil
		return v.local.Put(item)
	}
	if errors.Is(err, models.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if sameItem(current, item) {
		item.Version = stored.Version
		item.UpdatedAt = stored.UpdatedAt
		item.Dirty = false
		return v.local.Put(item)
	}

	current.Version = stored.Version
	current.Dirty = true
	return v.local.Put(current)
}

// markForRecreate drops the server version of a missing item, preserving any
// newer local edit. A deletion of an already missing item is simply finished.
func (v *Vault) markForRecreate(item models.Item) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	current, err := v.local.Get(item.ID)
	if errors.Is(err, models.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Deleted {
		return v.local.Delete(current.ID)
	}
	current.Version = 0
	current.Dirty = true
	if err := v.local.Put(current); err != nil {
		return err
	}
	return errRecreate
}

// sameItem reports whether the local item still equals the pushed snapshot.
func sameItem(a, b models.Item) bool {
	return a.ID == b.ID && a.Type == b.Type && a.Version == b.Version &&
		a.Deleted == b.Deleted && a.Dirty == b.Dirty &&
		a.UpdatedAt.Equal(b.UpdatedAt) && bytes.Equal(a.Data, b.Data)
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
