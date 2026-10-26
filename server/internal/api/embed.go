package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The web UI, built by `npm run build` in web/ into this directory (scripts/build.sh does both).
//
//go:embed all:dist
var dist embed.FS

func uiHandler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, "index.html"); err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte("The web UI is not built: run scripts/build.sh (it builds web/ into the server). The API is at /api/."))
			return
		}
		// Client-side routes all serve the app.
		if r.URL.Path != "/" && !strings.Contains(r.URL.Path[1:], ".") {
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}
