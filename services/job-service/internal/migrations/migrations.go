// Package migrations embeds the job-service SQL migrations into the binary,
// so the container needs no extra files or migrate CLI.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
