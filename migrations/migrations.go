// Package migrations embeds the SQL migrations of the server database, so
// the binary can bring the schema up to date without the files next to it.
package migrations

import "embed"

// FS holds the migration files.
//
//go:embed *.sql
var FS embed.FS
