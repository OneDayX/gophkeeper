package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// itemColumns are the columns scanItem expects, in its order.
const itemColumns = `id, type, data, version, deleted, updated_at`

// ItemRepository stores the encrypted items in PostgreSQL.
//
// Every write gives the item the next version of its owner. The version
// comes from the revision counter in the users table, bumped in the same
// transaction: the row lock queues the writes of one user, so their versions
// commit in increasing order, and a sync that asks for the changes after the
// version it has seen never misses one.
type ItemRepository struct {
	pool *pgxpool.Pool
}

// NewItemRepository returns an ItemRepository that runs its queries on pool.
func NewItemRepository(pool *pgxpool.Pool) *ItemRepository {
	return &ItemRepository{pool: pool}
}

// Create stores a new item of the user and returns it with its version and
// time. It returns models.ErrItemExists if the user already has an item with
// this ID, deleted or not.
func (r *ItemRepository) Create(ctx context.Context, userID int64, item models.Item) (models.Item, error) {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		version, err := nextVersion(ctx, tx, userID)
		if err != nil {
			return err
		}

		err = tx.QueryRow(ctx,
			`INSERT INTO items (user_id, id, type, data, version)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING version, updated_at`,
			userID, item.ID, int16(item.Type), item.Data, version,
		).Scan(&item.Version, &item.UpdatedAt)
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %s", models.ErrItemExists, item.ID)
		}

		return err
	})
	if err != nil {
		return models.Item{}, fmt.Errorf("create item: %w", err)
	}

	item.Deleted = false

	return item, nil
}

// Update replaces the data of an item if item.Version is still its current
// version, and returns the item with the new version and time; the type of
// the item never changes. It returns models.ErrVersionConflict if the item
// has another version and models.ErrItemNotFound if there is no such item or
// it has been deleted.
func (r *ItemRepository) Update(ctx context.Context, userID int64, item models.Item) (models.Item, error) {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		version, err := nextVersion(ctx, tx, userID)
		if err != nil {
			return err
		}

		var typ int16
		err = tx.QueryRow(ctx,
			`UPDATE items SET data = $3, version = $4, updated_at = now()
			 WHERE user_id = $1 AND id = $2 AND version = $5 AND NOT deleted
			 RETURNING type, version, updated_at`,
			userID, item.ID, item.Data, version, item.Version,
		).Scan(&typ, &item.Version, &item.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return explainMiss(ctx, tx, userID, item.ID, item.Version)
		}
		item.Type = models.ItemType(typ)

		return err
	})
	if err != nil {
		return models.Item{}, fmt.Errorf("update item: %w", err)
	}

	return item, nil
}

// Delete turns an item into a tombstone if version is still its current
// version, and returns the version of the tombstone. It fails like Update.
func (r *ItemRepository) Delete(ctx context.Context, userID int64, id string, version int64) (int64, error) {
	var deleted int64

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		next, err := nextVersion(ctx, tx, userID)
		if err != nil {
			return err
		}

		err = tx.QueryRow(ctx,
			`UPDATE items SET data = '', deleted = TRUE, version = $3, updated_at = now()
			 WHERE user_id = $1 AND id = $2 AND version = $4 AND NOT deleted
			 RETURNING version`,
			userID, id, next, version,
		).Scan(&deleted)
		if errors.Is(err, pgx.ErrNoRows) {
			return explainMiss(ctx, tx, userID, id, version)
		}

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("delete item: %w", err)
	}

	return deleted, nil
}

// Get returns an item of the user, or models.ErrItemNotFound if there is no
// such item or it has been deleted.
func (r *ItemRepository) Get(ctx context.Context, userID int64, id string) (models.Item, error) {
	item, err := scanItem(r.pool.QueryRow(ctx,
		`SELECT `+itemColumns+` FROM items WHERE user_id = $1 AND id = $2 AND NOT deleted`,
		userID, id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Item{}, fmt.Errorf("%w: %s", models.ErrItemNotFound, id)
	}
	if err != nil {
		return models.Item{}, fmt.Errorf("get item: %w", err)
	}

	return item, nil
}

// List calls fn for every item of the user with a version greater than
// since, tombstones included, in the order of versions. It stops at the
// first error of fn and returns it.
func (r *ItemRepository) List(ctx context.Context, userID int64, since int64, fn func(models.Item) error) error {
	rows, err := r.pool.Query(ctx,
		`SELECT `+itemColumns+` FROM items WHERE user_id = $1 AND version > $2 ORDER BY version`,
		userID, since,
	)
	if err != nil {
		return fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return fmt.Errorf("scan item: %w", err)
		}

		if err := fn(item); err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("list items: %w", err)
	}

	return nil
}

// nextVersion bumps the revision of the user and returns it as the version
// of the change tx is making. The row lock it takes holds the other writes
// of the user until tx ends.
func nextVersion(ctx context.Context, tx pgx.Tx, userID int64) (int64, error) {
	var version int64

	err := tx.QueryRow(ctx,
		`UPDATE users SET revision = revision + 1 WHERE id = $1 RETURNING revision`,
		userID,
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: %d", models.ErrUserNotFound, userID)
	}

	return version, err
}

// explainMiss finds out why a write conditioned on the version has matched
// no row: there is no such item, it has another version, or it is a
// tombstone.
func explainMiss(ctx context.Context, tx pgx.Tx, userID int64, id string, version int64) error {
	var (
		current int64
		deleted bool
	)

	err := tx.QueryRow(ctx,
		`SELECT version, deleted FROM items WHERE user_id = $1 AND id = $2`,
		userID, id,
	).Scan(&current, &deleted)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s", models.ErrItemNotFound, id)
	case err != nil:
		return err
	case current != version:
		return fmt.Errorf("%w: %s is at version %d, not %d", models.ErrVersionConflict, id, current, version)
	default:
		// The version matches, so the condition failed on the tombstone.
		return fmt.Errorf("%w: %s is deleted", models.ErrItemNotFound, id)
	}
}

// scanItem reads a row of itemColumns.
func scanItem(row pgx.Row) (models.Item, error) {
	var (
		item models.Item
		typ  int16
	)

	err := row.Scan(&item.ID, &typ, &item.Data, &item.Version, &item.Deleted, &item.UpdatedAt)
	item.Type = models.ItemType(typ)

	return item, err
}
