// Package web holds the pages and assets compiled into the binary.
package web

import "embed"

// Files is index.html (the phone remote), tv.html (the player) and static/.
//
//go:embed index.html tv.html static
var Files embed.FS
