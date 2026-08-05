// Package web embeds the built SPA so the binary ships as a single file.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
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
		name = "index.html"
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
