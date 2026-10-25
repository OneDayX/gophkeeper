package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/OneDayX/gophkeeper/internal/server/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const itemID = "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e01"

type fakeTokens struct{}

func (fakeTokens) Issue(userID int64) (string, error) {
	return fmt.Sprintf("token-%d", userID), nil
}

func TestUserService(t *testing.T) {
	svc := NewUserService(repository.NewMemoryUserRepository(), fakeTokens{})
	ctx := context.Background()

	token, err := svc.Register(ctx, "alice", "secret")
	require.NoError(t, err)
	assert.Equal(t, "token-1", token)

	_, err = svc.Register(ctx, "alice", "other")
	assert.ErrorIs(t, err, models.ErrLoginTaken)

	token, err = svc.Login(ctx, "alice", "secret")
	require.NoError(t, err)
	assert.Equal(t, "token-1", token)

	_, err = svc.Login(ctx, "alice", "wrong")
	assert.ErrorIs(t, err, models.ErrInvalidCredentials)

	_, err = svc.Login(ctx, "bob", "secret")
	assert.ErrorIs(t, err, models.ErrInvalidCredentials)
}

func TestUserService_MalformedCredentials(t *testing.T) {
	svc := NewUserService(repository.NewMemoryUserRepository(), fakeTokens{})
	ctx := context.Background()

	for _, c := range []struct{ login, password string }{
		{"", "secret"},
		{"alice", ""},
		{strings.Repeat("a", maxLoginLength+1), "secret"},
		{"alice", strings.Repeat("p", 73)},
	} {
		_, err := svc.Register(ctx, c.login, c.password)
		assert.ErrorIs(t, err, models.ErrMalformedCredentials)

		_, err = svc.Login(ctx, c.login, c.password)
		assert.ErrorIs(t, err, models.ErrMalformedCredentials)
	}
}

type brokenUsers struct{}

func (brokenUsers) Create(context.Context, string, string) (int64, error) {
	return 0, errors.New("disk is full")
}

func (brokenUsers) GetByLogin(context.Context, string) (models.User, error) {
	return models.User{}, errors.New("disk is full")
}

func TestUserService_StoreFailure(t *testing.T) {
	svc := NewUserService(brokenUsers{}, fakeTokens{})

	_, err := svc.Register(context.Background(), "alice", "secret")
	assert.Error(t, err)

	_, err = svc.Login(context.Background(), "alice", "secret")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, models.ErrInvalidCredentials, "a broken store is not a wrong password")
}

func TestItemService(t *testing.T) {
	svc := NewItemService(repository.NewMemoryItemRepository())
	ctx := context.Background()

	// The ID is normalized, so an uppercase one names the same item.
	created, err := svc.Create(ctx, 1, models.Item{ID: strings.ToUpper(itemID), Type: models.ItemTypeText, Data: []byte("v1")})
	require.NoError(t, err)
	assert.Equal(t, itemID, created.ID)
	assert.Equal(t, int64(1), created.Version)

	got, err := svc.Get(ctx, 1, itemID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), got.Data)

	updated, err := svc.Update(ctx, 1, models.Item{ID: itemID, Data: []byte("v2"), Version: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)

	_, err = svc.Update(ctx, 1, models.Item{ID: itemID, Data: []byte("v3"), Version: 1})
	assert.ErrorIs(t, err, models.ErrVersionConflict)

	version, err := svc.Delete(ctx, 1, itemID, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), version)

	var listed []models.Item
	require.NoError(t, svc.List(ctx, 1, 0, func(item models.Item) error {
		listed = append(listed, item)
		return nil
	}))
	require.Len(t, listed, 1)
	assert.True(t, listed[0].Deleted)
}

func TestItemService_GeneratesID(t *testing.T) {
	svc := NewItemService(repository.NewMemoryItemRepository())

	created, err := svc.Create(context.Background(), 1, models.Item{Type: models.ItemTypeCard, Data: []byte("card")})
	require.NoError(t, err)

	_, err = normalizeID(created.ID)
	assert.NoError(t, err)
}

func TestItemService_InvalidRequests(t *testing.T) {
	svc := NewItemService(repository.NewMemoryItemRepository())
	ctx := context.Background()
	data := []byte("data")

	cases := map[string]error{}
	_, cases["create with a bad ID"] = svc.Create(ctx, 1, models.Item{ID: "not-a-uuid", Type: models.ItemTypeText, Data: data})
	_, cases["create with an unknown type"] = svc.Create(ctx, 1, models.Item{ID: itemID, Type: 42, Data: data})
	_, cases["create without data"] = svc.Create(ctx, 1, models.Item{ID: itemID, Type: models.ItemTypeText})
	_, cases["create with huge data"] = svc.Create(ctx, 1, models.Item{ID: itemID, Type: models.ItemTypeText, Data: make([]byte, models.MaxItemSize+1)})
	_, cases["update with a bad ID"] = svc.Update(ctx, 1, models.Item{ID: "x", Data: data, Version: 1})
	_, cases["update without a version"] = svc.Update(ctx, 1, models.Item{ID: itemID, Data: data})
	_, cases["update without data"] = svc.Update(ctx, 1, models.Item{ID: itemID, Version: 1})
	_, cases["delete with a bad ID"] = svc.Delete(ctx, 1, "x", 1)
	_, cases["delete without a version"] = svc.Delete(ctx, 1, itemID, 0)
	_, cases["get with a bad ID"] = svc.Get(ctx, 1, "x")
	cases["list with a negative version"] = svc.List(ctx, 1, -1, func(models.Item) error { return nil })

	for name, err := range cases {
		assert.ErrorIs(t, err, models.ErrInvalidItem, name)
	}
}
