// Package web embeds the built SPA so the binary ships as a single file.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

// dist holds the Vite build output. The repo carries a placeholder index.html
// so `go build` works on a fresh checkout without running the UI build first;
// the Containerfile overwrites the directory with the real bundle.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA, falling back to index.html for unknown
// paths so client-side routes survive a hard reload.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return spaHandler{files: http.FS(sub), fsys: sub}, nil
}

type spaHandler struct {
	files http.FileSystem
	fsys  fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(h.fsys, name); err != nil {
		// Unknown path. A deep link must land on the SPA instead of an error
		// page, so it gets the shell — but a MISSING ASSET must not.
		//
		// Falling back indiscriminately answers /assets/index-OLD.js with
		// index.html, 200, Content-Type text/html. A browser holding a stale
		// shell across a deploy then parses HTML as JavaScript, dies on
		// "Unexpected token '<'" and shows a white page, where a plain 404
		// would have let it recover on reload. The same applies to
		// /favicon.ico and every other extensioned request.
		//
		// A file extension is the discriminator rather than the Accept header:
		// client-side routes are extensionless by construction, while every
		// bundled asset is hashed and ends in one. Accept is not reliably sent
		// on a hard navigation.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/"
	}
	http.FileServer(h.files).ServeHTTP(w, r)
}
