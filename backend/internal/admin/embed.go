// Package admin serves the embedded Gateway administration interface.
package admin

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed assets/*
var assets embed.FS

// Handler serves the Angular application at paths relative to /admin/.
// Management data remains authenticated by /admin/api, outside this handler.
func Handler() http.Handler {
	root, err := fs.Sub(assets, "assets/ui")
	if err != nil {
		panic(err)
	}
	return spaHandler(root)
}

func securityHeaders(w http.ResponseWriter, stylePolicy string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src "+stylePolicy+"; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
}

func spaHandler(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w, "'self'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		for _, segment := range strings.Split(name, "/") {
			if segment == ".." || segment == "." {
				http.NotFound(w, r)
				return
			}
		}
		if strings.HasPrefix(name, "api/") || name == "api" || strings.ContainsRune(name, '\x00') {
			http.NotFound(w, r)
			return
		}
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(root, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		index, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "Gateway interface has not been built. Build frontend and embed it before starting this binary.", http.StatusServiceUnavailable)
			return
		}
		var random [24]byte
		if _, err := rand.Read(random[:]); err != nil {
			http.Error(w, "Gateway interface is temporarily unavailable.", http.StatusServiceUnavailable)
			return
		}
		nonce := base64.RawStdEncoding.EncodeToString(random[:])
		securityHeaders(w, "'self' 'nonce-"+nonce+"'")
		index = bytes.ReplaceAll(index, []byte("__GATEWAY_CSP_NONCE__"), []byte(nonce))
		// The application builder may inline critical styles into the document.
		index = bytes.ReplaceAll(index, []byte("<style>"), []byte("<style nonce=\""+nonce+"\">"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(index)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(index)
		}
	})
}

// LegacyHandler retains the existing first-run and recovery setup interface.
// It is supplied only to the bootstrap manager, before the runtime is active.
func LegacyHandler() http.Handler {
	root, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w, "'self'")
		files.ServeHTTP(w, r)
	})
}
