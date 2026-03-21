// Package store provides the data-access layer for glitchgate.
package store

import "embed"

//go:embed migrations/*.sql
var migrations embed.FS
