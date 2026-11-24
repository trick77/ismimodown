// Package web embeds the built SPA so the binary ships as a single file.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"sync"

	"github.com/trick77/ship/kit/spa"
	"github.com/trick77/toolkit/analytics"
)

// dist holds the Vite build output. The repo carries only dist/.gitkeep, so
// `go build` works on a fresh checkout without running the UI build first (kit
// serves its 503 placeholder then); the Containerfile copies the real bundle in.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA. Unknown paths 404; there is nothing to fall
// back to, because this site has exactly one URL — see isShellRoute.
//
// analyticsOrigin, when set (config.AnalyticsOrigin), puts the Trakk
// collector's browser script into the shell.
func Handler(analyticsOrigin string) (http.Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return handler(sub, analyticsOrigin), nil
}

// handler is kit's SPA handler over dist: real files from the bundle (hashed
// assets/ cached a year, immutable), the shell for "/" alone, 404 for the rest.
func handler(dist fs.FS, analyticsOrigin string) http.Handler {
	o := spa.Options{Routes: isShellRoute}
	if analyticsOrigin != "" {
		o.Index = analyticsScript(analyticsOrigin)
	}
	return spa.Handler(dist, o)
}

// analyticsScript adds the Trakk browser script to the shell, right before
// </head>.
//
// Injected by the server, not written into ui/index.html: the collector's host
// is a deployment setting kept out of this public repo, and the script finds
// its collector by its own src, so it has to load from there. defer, so it
// never holds up the page. The tag is built once; the shell is the same bytes
// for every request.
//
// A shell without </head> is served as it is: no tag beats a tag in the wrong
// place.
func analyticsScript(origin string) func([]byte, *http.Request) []byte {
	tag := []byte(`<script src="` + origin + analytics.ScriptPath + `" defer></script>`)
	var once sync.Once
	var out []byte
	return func(index []byte, _ *http.Request) []byte {
		once.Do(func() {
			i := bytes.LastIndex(index, []byte("</head>"))
			if i < 0 {
				out = index
				return
			}
			out = make([]byte, 0, len(index)+len(tag))
			out = append(out, index[:i]...)
			out = append(out, tag...)
			out = append(out, index[i:]...)
		})
		return out
	}
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
