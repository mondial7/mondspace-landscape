// Package web embeds the browser UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var assets embed.FS

// FS returns the UI files rooted at the static directory.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err) // the embed directive guarantees the directory exists
	}
	return sub
}

// Handler serves the UI: index.html at "/" and assets under "/static/".
func Handler() http.Handler {
	files := http.FileServer(http.FS(FS()))
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", files))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, FS(), "index.html")
	})
	return mux
}
