// Package filters embeds the built-in filter definitions. The .toml files are
// loaded and compiled by internal/filter.
package filters

import "embed"

// FS holds the built-in filter TOML files, embedded at compile time.
//
//go:embed *.toml
var FS embed.FS
