// Package repository keeps the server data: users and their encrypted items.
// The PostgreSQL repositories are the real storage; the memory ones follow
// the same rules and serve tests and quick runs without a database.
//
// The repositories turn storage facts, such as a taken unique key, into the
// errors of the models package and hold no business rules.
package repository

import (
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation reports whether the statement failed on a unique key.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}
