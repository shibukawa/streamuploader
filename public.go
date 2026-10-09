// Package streamuploader is the module root. Its one job is to embed the
// built public asset tree that Popcorn Web serves at server.public.mount: the
// tree is written by `go tool pw generate` into dist/public from the files
// under public/, and registered here so that no handler has to know about it.
package streamuploader

import (
	"embed"
	"io/fs"

	"github.com/shibukawa/popcornweb/middlewares"
)

//go:embed all:dist/public
var embeddedPublic embed.FS

func init() {
	middlewares.RegisterPublicFS(PublicFS())
}

// PublicFS is the built public tree, rooted at its public directory.
func PublicFS() fs.FS {
	result, err := fs.Sub(embeddedPublic, "dist/public")
	if err != nil {
		panic(err)
	}
	return result
}
