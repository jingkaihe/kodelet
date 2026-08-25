// Package webui provides Kodelet's embedded browser application.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pkg/errors"
)

//go:generate bash -c "cd frontend && npm install && npm run build"
//go:embed all:dist/*
var embedFS embed.FS

// Handler serves Kodelet's embedded browser application.
type Handler struct {
	assets       http.Handler
	indexContent []byte
}

// NewHandler creates an HTTP handler for the embedded Web UI assets and SPA.
func NewHandler() (*Handler, error) {
	assets, err := fs.Sub(embedFS, "dist/assets")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Web UI asset filesystem")
	}
	indexContent, err := embedFS.ReadFile("dist/index.html")
	if err != nil {
		return nil, errors.Wrap(err, "failed to read Web UI index")
	}

	return &Handler{
		assets:       http.StripPrefix("/assets/", http.FileServer(http.FS(assets))),
		indexContent: indexContent,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		h.assets.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(h.indexContent)
}

// IsPublicPath reports whether path is a static browser resource that must load
// before the user has authenticated with the control plane.
func (h *Handler) IsPublicPath(path string) bool {
	return h != nil && (strings.HasPrefix(path, "/assets/") || path == "/favicon.ico")
}
