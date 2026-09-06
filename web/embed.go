package web

import "embed"

// StaticFiles embeds all web dashboard files (HTML, CSS, JS) into the Go binary.
// This allows the entire application to be deployed as a single executable.
//
//go:embed index.html css js
var StaticFiles embed.FS
