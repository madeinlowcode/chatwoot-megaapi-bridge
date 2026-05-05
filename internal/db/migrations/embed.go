// Package migrations exposes the SQL migration files as an embedded filesystem.
// The canonical SQL lives at the repo root in migrations/; this package re-exposes
// it via go:embed so the bridge binary can run goose migrations without needing
// the source tree at runtime.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
