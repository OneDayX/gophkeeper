package database

import (
	"errors"
	"net/url"

	"github.com/OneDayX/gophkeeper/migrations"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // postgres driver for migrate
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrateSchema applies all pending migrations from the embedded package.
func migrateSchema(dsn string) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	// The driver registers itself under its own scheme, whatever the DSN
	// says: postgres:// and postgresql:// are both common.
	u.Scheme = "pgx5"

	m, err := migrate.NewWithSourceInstance("iofs", source, u.String())
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}
