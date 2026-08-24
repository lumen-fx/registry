// Package migrations holds the database migrations and embeds them, so a
// binary carries its own schema history.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
