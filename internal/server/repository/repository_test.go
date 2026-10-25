package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/OneDayX/gophkeeper/internal/server/database"
	"github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	idA = "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e01"
	idB = "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e02"
)

type userRepo interface {
	Create(ctx context.Context, login, passwordHash string) (int64, error)
	GetByLogin(ctx context.Context, login string) (models.User, error)
}

type itemRepo interface {
	Create(ctx context.Context, userID int64, item models.Item) (models.Item, error)
	Update(ctx context.Context, userID int64, item models.Item) (models.Item, error)
	Delete(ctx context.Context, userID int64, id string, version int64) (int64, error)
	Get(ctx context.Context, userID int64, id string) (models.Item, error)
	List(ctx context.Context, userID int64, since int64, fn func(models.Item) error) error
}

// repos is a pair of repositories of one implementation.
type repos struct {
	users userRepo
	items itemRepo
}

// implementations lists the repositories every test runs against. The
// PostgreSQL ones are skipped unless TEST_DATABASE_DSN is set.
var implementations = map[string]func(t *testing.T) repos{
	"memory": func(*testing.T) repos {
		return repos{users: NewMemoryUserRepository(), items: NewMemoryItemRepository()}
	},
	"postgres": func(t *testing.T) repos {
		pool := newTestPool(t)
		return repos{users: NewUserRepository(pool), items: NewItemRepository(pool)}
	},
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set, skipping database tests")
	}

	db, err := database.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(db.Close)

	_, err = db.Pool().Exec(context.Background(), `TRUNCATE users, items RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	return db.Pool()
}

// forEach runs the test against every implementation.
func forEach(t *testing.T, test func(t *testing.T, r repos)) {
	for name, newRepos := range implementations {
		t.Run(name, func(t *testing.T) {
			test(t, newRepos(t))
		})
	}
}

// list collects what List gives.
func list(t *testing.T, r repos, userID, since int64) []models.Item {
	t.Helper()

	var items []models.Item
	require.NoError(t, r.items.List(context.Background(), userID, since, func(item models.Item) error {
		items = append(items, item)
		return nil
	}))

	return items
}

func TestUsers(t *testing.T) {
	forEach(t, func(t *testing.T, r repos) {
		ctx := context.Background()

		id, err := r.users.Create(ctx, "alice", "hash")
		require.NoError(t, err)

		_, err = r.users.Create(ctx, "alice", "hash")
		assert.ErrorIs(t, err, models.ErrLoginTaken)

		user, err := r.users.GetByLogin(ctx, "alice")
		require.NoError(t, err)
		assert.Equal(t, models.User{ID: id, Login: "alice", PasswordHash: "hash"}, user)

		_, err = r.users.GetByLogin(ctx, "bob")
		assert.ErrorIs(t, err, models.ErrUserNotFound)
	})
}

func TestItems_CreateAndGet(t *testing.T) {
	forEach(t, func(t *testing.T, r repos) {
		ctx := context.Background()
		alice, err := r.users.Create(ctx, "alice", "hash")
		require.NoError(t, err)
		bob, err := r.users.Create(ctx, "bob", "hash")
		require.NoError(t, err)

		a, err := r.items.Create(ctx, alice, models.Item{ID: idA, Type: models.ItemTypeCard, Data: []byte("a")})
		require.NoError(t, err)
		assert.Equal(t, int64(1), a.Version)
		assert.False(t, a.UpdatedAt.IsZero())

		b, err := r.items.Create(ctx, alice, models.Item{ID: idB, Type: models.ItemTypeText, Data: []byte("b")})
		require.NoError(t, err)
		assert.Equal(t, int64(2), b.Version, "versions grow per user")

		_, err = r.items.Create(ctx, alice, models.Item{ID: idA, Type: models.ItemTypeText, Data: []byte("again")})
		assert.ErrorIs(t, err, models.ErrItemExists)

		// IDs are scoped by user, and so are versions.
		bobs, err := r.items.Create(ctx, bob, models.Item{ID: idA, Type: models.ItemTypeOTP, Data: []byte("bob")})
		require.NoError(t, err)
		assert.Equal(t, int64(1), bobs.Version)

		got, err := r.items.Get(ctx, alice, idA)
		require.NoError(t, err)
		assert.Equal(t, idA, got.ID)
		assert.Equal(t, models.ItemTypeCard, got.Type)
		assert.Equal(t, []byte("a"), got.Data)
		assert.Equal(t, int64(1), got.Version)
		assert.False(t, got.Deleted)

		got, err = r.items.Get(ctx, bob, idA)
		require.NoError(t, err)
		assert.Equal(t, []byte("bob"), got.Data)

		_, err = r.items.Get(ctx, bob, idB)
		assert.ErrorIs(t, err, models.ErrItemNotFound)
	})
}

func TestItems_UpdateAndDelete(t *testing.T) {
	forEach(t, func(t *testing.T, r repos) {
		ctx := context.Background()
		alice, err := r.users.Create(ctx, "alice", "hash")
		require.NoError(t, err)

		_, err = r.items.Create(ctx, alice, models.Item{ID: idA, Type: models.ItemTypeCard, Data: []byte("v1")})
		require.NoError(t, err)

		updated, err := r.items.Update(ctx, alice, models.Item{ID: idA, Data: []byte("v2"), Version: 1})
		require.NoError(t, err)
		assert.Equal(t, int64(2), updated.Version)
		assert.Equal(t, models.ItemTypeCard, updated.Type, "the type stays")

		// Another client still has version 1.
		_, err = r.items.Update(ctx, alice, models.Item{ID: idA, Data: []byte("stale"), Version: 1})
		assert.ErrorIs(t, err, models.ErrVersionConflict)
		_, err = r.items.Delete(ctx, alice, idA, 1)
		assert.ErrorIs(t, err, models.ErrVersionConflict)

		_, err = r.items.Update(ctx, alice, models.Item{ID: idB, Data: []byte("x"), Version: 1})
		assert.ErrorIs(t, err, models.ErrItemNotFound)
		_, err = r.items.Delete(ctx, alice, idB, 1)
		assert.ErrorIs(t, err, models.ErrItemNotFound)

		got, err := r.items.Get(ctx, alice, idA)
		require.NoError(t, err)
		assert.Equal(t, []byte("v2"), got.Data, "failed writes change nothing")

		tombstone, err := r.items.Delete(ctx, alice, idA, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(3), tombstone, "failed writes take no version")

		_, err = r.items.Get(ctx, alice, idA)
		assert.ErrorIs(t, err, models.ErrItemNotFound)
		_, err = r.items.Update(ctx, alice, models.Item{ID: idA, Data: []byte("v4"), Version: 3})
		assert.ErrorIs(t, err, models.ErrItemNotFound)
		_, err = r.items.Delete(ctx, alice, idA, 3)
		assert.ErrorIs(t, err, models.ErrItemNotFound)
		_, err = r.items.Create(ctx, alice, models.Item{ID: idA, Type: models.ItemTypeCard, Data: []byte("v5")})
		assert.ErrorIs(t, err, models.ErrItemExists, "a tombstone keeps its ID")
	})
}

func TestItems_List(t *testing.T) {
	forEach(t, func(t *testing.T, r repos) {
		ctx := context.Background()
		alice, err := r.users.Create(ctx, "alice", "hash")
		require.NoError(t, err)
		bob, err := r.users.Create(ctx, "bob", "hash")
		require.NoError(t, err)

		assert.Empty(t, list(t, r, alice, 0))

		_, err = r.items.Create(ctx, alice, models.Item{ID: idA, Type: models.ItemTypeText, Data: []byte("a")})
		require.NoError(t, err)
		_, err = r.items.Create(ctx, alice, models.Item{ID: idB, Type: models.ItemTypeText, Data: []byte("b")})
		require.NoError(t, err)
		_, err = r.items.Create(ctx, bob, models.Item{ID: idA, Type: models.ItemTypeText, Data: []byte("bob")})
		require.NoError(t, err)
		// A goes to version 3 and then becomes a tombstone at 4.
		_, err = r.items.Update(ctx, alice, models.Item{ID: idA, Data: []byte("a2"), Version: 1})
		require.NoError(t, err)
		_, err = r.items.Delete(ctx, alice, idA, 3)
		require.NoError(t, err)

		all := list(t, r, alice, 0)
		require.Len(t, all, 2)
		assert.Equal(t, idB, all[0].ID, "ordered by version")
		assert.Equal(t, int64(2), all[0].Version)
		assert.Equal(t, []byte("b"), all[0].Data)
		assert.Equal(t, idA, all[1].ID)
		assert.Equal(t, int64(4), all[1].Version)
		assert.True(t, all[1].Deleted)
		assert.Empty(t, all[1].Data)

		since := list(t, r, alice, 2)
		require.Len(t, since, 1)
		assert.Equal(t, idA, since[0].ID)

		assert.Empty(t, list(t, r, alice, 4))

		stop := errors.New("stop")
		calls := 0
		err = r.items.List(ctx, alice, 0, func(models.Item) error {
			calls++
			return stop
		})
		assert.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})
}

// TestItems_ConcurrentWrites checks that parallel writes of one user get
// distinct versions and that all of them are listed.
func TestItems_ConcurrentWrites(t *testing.T) {
	forEach(t, func(t *testing.T, r repos) {
		ctx := context.Background()
		alice, err := r.users.Create(ctx, "alice", "hash")
		require.NoError(t, err)

		ids := []string{
			"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002",
			"00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000004",
			"00000000-0000-4000-8000-000000000005", "00000000-0000-4000-8000-000000000006",
			"00000000-0000-4000-8000-000000000007", "00000000-0000-4000-8000-000000000008",
		}

		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Go(func() {
				_, err := r.items.Create(ctx, alice, models.Item{ID: id, Type: models.ItemTypeText, Data: []byte(id)})
				assert.NoError(t, err)
			})
		}
		wg.Wait()

		items := list(t, r, alice, 0)
		require.Len(t, items, len(ids))
		for i, item := range items {
			assert.Equal(t, int64(i+1), item.Version)
		}
	})
}

func TestMemoryItemRepository_ListStopsOnCancel(t *testing.T) {
	r := NewMemoryItemRepository()
	_, err := r.Create(context.Background(), 1, models.Item{ID: idA, Type: models.ItemTypeText, Data: []byte("a")})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = r.List(ctx, 1, 0, func(models.Item) error { return nil })
	assert.ErrorIs(t, err, context.Canceled)
}

func TestItemRepository_UnknownUser(t *testing.T) {
	items := NewItemRepository(newTestPool(t))

	_, err := items.Create(context.Background(), 42, models.Item{ID: idA, Type: models.ItemTypeText, Data: []byte("a")})
	assert.ErrorIs(t, err, models.ErrUserNotFound)
}
