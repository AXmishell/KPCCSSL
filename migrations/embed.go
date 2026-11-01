// Package migrations embeds the SQL migration files so the binary is
// self-contained.
package migrations

import "embed"

// FS contains every goose migration in this directory.
//
//go:embed *.sql
var FS embed.FS
