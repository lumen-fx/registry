// Package web holds the registry's browser UI: one page, the two libraries it
// renders README markdown with, and the CLI installer. All of it is embedded,
// so the server binary serves it with nothing extra to deploy and no build
// step.
package web

import "embed"

//go:embed index.html
var IndexHTML []byte

//go:embed install.sh
var InstallScript []byte

// The README panel renders markdown in the browser, so the libraries it uses
// are embedded beside the page rather than loaded from a CDN. Their file names
// carry their version, so a reader caches them for good and an upgrade is a
// new name.
//
//go:embed assets/*.js
var Assets embed.FS
