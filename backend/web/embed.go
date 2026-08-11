// Package web embeds the built SPA so the binary ships as a single file.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

// Go's built-in table has no entry for .webmanifest, so http.FileServer would
// sniff manifest.webmanifest and serve it as text/plain — which Chrome rejects,
// silently dropping the PWA icons and theme colour. Registered here rather than
// special-cased in the handler, so the FileServer keeps doing the content-type
// work.
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// dist holds the Vite build output. The repo carries a placeholder index.html
// so `go build` works on a fresh checkout without running the UI build first;
// the Containerfile overwrites the directory with the real bundle.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA. Unknown paths 404; there is nothing to fall
// back to, because this site has exactly one URL — see spaHandler.
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
		// Unknown path: 404, whatever it looks like.
		//
		// This used to serve index.html with a 200 for any EXTENSIONLESS
		// unknown path, on the standard SPA argument that a deep link must
		// survive a hard reload rather than land on an error page. That
		// argument does not apply here: this app has no client-side router and
		// exactly one URL. Its state lives in the query string — see
		// ui/src/App.tsx — so no visitor-supplied word ever appears in a path
		// position, and the fallback protected no real link.
		//
		// What it did instead was answer every wrong URL on the host with the
		// homepage and a 200: a soft 404, and an unbounded set of duplicates of
		// the one page this site has. The canonical link in index.html tells a
		// search engine to collapse them; Bing in particular would rather be
		// told 404 outright, and treats a host that 200s everything as a
		// quality signal in its own right.
		//
		// The extensioned half was already a 404 and stays one for its own
		// reason: falling back there answers /assets/index-OLD.js with
		// index.html, 200, Content-Type text/html. A browser holding a stale
		// shell across a deploy then parses HTML as JavaScript, dies on
		// "Unexpected token '<'" and shows a white page, where a plain 404 lets
		// it recover on reload.
		http.NotFound(w, r)
		return
	}
	setCacheControl(w, name)
	http.FileServer(h.files).ServeHTTP(w, r)
}

// assetPrefix is the directory Vite writes its hashed output to. Everything
// under it — the JS chunk, the stylesheet, the three woff2 — carries a content
// hash in its filename, which is what makes a year-long immutable cache safe:
// a rebuild changes the name, so there is no such thing as a stale hit.
const assetPrefix = "assets/"

// setCacheControl tells the browser how long it may keep what it just fetched.
//
// http.FileServer sets neither Cache-Control nor a usable validator here: the
// files come from an embed.FS, whose entries have a ZERO modtime, so it emits
// no Last-Modified either. Every visit therefore re-downloaded the whole
// bundle — 700 kB of JS, CSS and fonts that had not changed in weeks.
//
// Two rules, and the split matters more than either value:
//
//   - Hashed assets: a year, immutable. immutable additionally suppresses the
//     revalidation a browser would otherwise send on a reload.
//   - Everything else — index.html above all, but also the icons and the
//     manifest at the root, which are NOT hashed: no-cache. Not "no caching":
//     the copy may be stored and reused, but only after asking. index.html is
//     the file that names the current hashed bundle, so a cached one is a
//     browser pinned to a deleted build; the 404 branch above is the recovery
//     path, and it should not have to fire in the first place.
func setCacheControl(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, assetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}
