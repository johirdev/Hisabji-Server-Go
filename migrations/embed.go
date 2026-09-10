// Package migrations embeds the SQL schema files into the binary.
//
// Embedding matters for deployment: the container image is a single static
// binary with no /migrations directory to forget to COPY, and the schema can
// never drift from the code that expects it.
package migrations

import "embed"

// FS holds every .sql migration, applied in filename order.
//
//go:embed *.sql
var FS embed.FS
