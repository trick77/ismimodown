// Package web embeds the built SPA so the binary ships as a single file.
package web

import (
	"embed"
	"io/fs"
	"net/http"
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
		// Unknown path: hand back the shell rather than a 404, so a deep link
		// reloaded in the browser lands on the SPA instead of an error page.
		r = r.Clone(r.Context())
		r.URL.Path = "/"
	}
	http.FileServer(h.files).ServeHTTP(w, r)
}
