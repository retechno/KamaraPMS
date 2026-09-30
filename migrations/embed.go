// Package migrations embeds the SQL schema migrations (goose format) so the
// binaries and tests always apply exactly the schema in this directory.
package migrations

import "embed"

// FS contains every *.sql migration at its root.
//
//go:embed *.sql
var FS embed.FS
