// Package migrations embeds the SQL migration files into the binary so the
// container image needs no extra files to migrate the database.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
