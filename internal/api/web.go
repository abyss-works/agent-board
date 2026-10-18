package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const (
	webIndex = "index.html"

	// Vite names every file under assets/ after its content, so a browser may
	// keep it indefinitely.
	cacheImmutable = "public, max-age=31536000, immutable"
	// Every other file is checked with the server on each use.
	cacheRevalidate = "no-cache"
)

// webHandler serves the built web client from fsys. An existing file is sent
// as it is. A missing path without a file extension is a route of the single
// page app and gets index.html; a missing path with an extension is a 404.
// Directories are never listed. Only GET and HEAD are allowed.
func webHandler(fsys fs.FS) http.Handler {
	files := http.FS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Cleaning a rooted path removes every ".." element, and fsys rejects
		// whatever would still point outside of it.
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = webIndex
		}
		f, info := openFile(files, name)
		if f == nil && path.Ext(name) == "" {
			name = webIndex
			f, info = openFile(files, name)
		}
		if f == nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Cache-Control", cacheControl(name))
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
}

// openFile opens name as a regular file. A missing path, a directory and any
// other failure to open all yield nil.
func openFile(files http.FileSystem, name string) (http.File, fs.FileInfo) {
	f, err := files.Open(name)
	if err != nil {
		return nil, nil
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		f.Close()
		return nil, nil
	}
	return f, info
}

func cacheControl(name string) string {
	if strings.HasPrefix(name, "assets/") {
		return cacheImmutable
	}
	return cacheRevalidate
}
