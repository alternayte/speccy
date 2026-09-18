// Package web embeds the built React SPA.
package web

import "embed"

// Dist holds the Vite build output. `just build` fills it before the Go build.
//
//go:embed all:dist
var Dist embed.FS
