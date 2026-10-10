// Package webui embeds the built web UI. `npm run build` in web/ writes to
// dist/; if it has not been run, a placeholder page is served instead, so the
// binary always builds and the API still works.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

//go:embed placeholder
var placeholder embed.FS

// FS is the UI's static files, rooted at the site root.
func FS() fs.FS {
	if sub, err := fs.Sub(dist, "dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	sub, err := fs.Sub(placeholder, "placeholder")
	if err != nil {
		panic(err) // embedded at build time
	}
	return sub
}
