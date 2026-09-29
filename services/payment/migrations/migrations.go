// Package migrations embeds payment-svc's SQL migrations (golang-migrate format).
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
