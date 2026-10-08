// Package ui embeds the built web UI. Run `npm run build` in drive/ui to
// refresh dist/ after changing src/.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed index.html
var IndexHTML []byte

//go:embed dist
var distFS embed.FS

// Dist is the built asset tree served under /ui/.
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
