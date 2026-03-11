// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import "embed"

// Templates contains the embedded HTML template files.
//
//go:embed templates/*.html templates/fragments/*.html
var Templates embed.FS

// Static contains the embedded static assets.
//
//go:embed static/*
var Static embed.FS
