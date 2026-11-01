package database

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set, skipping database tests")
	}

	db, err := New(context.Background(), dsn)
	require.NoError(t, err)
	defer db.Close()

	// The migrations have created the tables.
	var tables int
	err = db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('users', 'items')`,
	).Scan(&tables)
	require.NoError(t, err)
	assert.Equal(t, 2, tables)

	// A second start finds nothing to migrate.
	again, err := New(context.Background(), dsn)
	require.NoError(t, err)
	again.Close()
}

func TestNew_BadDSN(t *testing.T) {
	_, err := New(context.Background(), "postgres://%zz")
	assert.Error(t, err)

	// A well-formed DSN of a database that is not there fails on migrations.
	_, err = New(context.Background(), "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	assert.Error(t, err)
}
