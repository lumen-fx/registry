// Package web holds the registry's browser UI. It is embedded so the server
// binary serves it with nothing extra to deploy, and it stays a single file
// with no build step.
package web

import _ "embed"

//go:embed index.html
var IndexHTML []byte

//go:embed install.sh
var InstallScript []byte
