// Package web embeds the built SPA so the binary ships as a single file.
package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/trick77/ship/kit/spa"
)

// dist holds the Vite build output. The repo carries only dist/.gitkeep, so
// `go build` works on a fresh checkout without running the UI build first (kit
// serves its 503 placeholder then); the Containerfile copies the real bundle in.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA. Unknown paths 404; there is nothing to fall
// back to, because this site has exactly one URL — see isShellRoute.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return handler(sub), nil
}

// handler is kit's SPA handler over dist: real files from the bundle (hashed
// assets/ cached a year, immutable), the shell for "/" alone, 404 for the rest.
func handler(dist fs.FS) http.Handler {
	return spa.Handler(dist, spa.Options{Routes: isShellRoute})
}

// isShellRoute says which unknown paths get the shell: "/" and nothing else.
//
// Kit's default answers every path with index.html, on the standard SPA
// argument that a deep link must survive a hard reload rather than land on an
// error page. That argument does not apply here: this app has no client-side
// router and exactly one URL. Its state lives in the query string — see
// ui/src/App.tsx — so no visitor-supplied word ever appears in a path position,
// and the fallback would protect no real link.
//
// What it would do instead is answer every wrong URL on the host with the
// homepage and a 200: a soft 404, and an unbounded set of duplicates of the one
// page this site has. The canonical link in index.html tells a search engine to
// collapse them; Bing in particular would rather be told 404 outright, and
// treats a host that 200s everything as a quality signal in its own right.
func isShellRoute(p string) bool {
	return p == "/"
}
