// Package uitest runs the native ESM UI suite in an isolated headless browser.
// Its HTTP server exposes an immutable allowlisted source snapshot, not a repo
// FileServer, application endpoints, configuration, or runtime user data.
package uitest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var sourceTrees = []string{"internal/webgui", "internal/system/uilang", "scripts", "test/ui"}

const (
	maxSourceSize     = 8 << 20
	maxSnapshotSize   = 64 << 20
	emptyTestDocument = "<!doctype html><html><head><meta charset=\"utf-8\"><title>SuperCli UI tests</title></head><body></body></html>"
)

func sourceExtension(name string) bool {
	switch path.Ext(name) {
	case ".js", ".css", ".html", ".json", ".go":
		return true
	}
	return false
}

func privateSourceName(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "supercli-data") || strings.HasPrefix(name, "auth-") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "bin", "dist", "logs", "tmp", "temp", "config", "data", "auth.json", "credentials.json", "config.json", "settings.json":
		return true
	}
	return false
}

func snapshot(root string) (map[string]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	sources := make(map[string]string)
	total := 0
	for _, tree := range sourceTrees {
		// Check every ancestor too: WalkDir itself does not follow symlinks,
		// but would otherwise follow an intermediate symlink in its root.
		directory := root
		missing := false
		for _, part := range strings.Split(tree, "/") {
			directory = filepath.Join(directory, part)
			info, err := os.Lstat(directory)
			if errors.Is(err, os.ErrNotExist) {
				missing = true
				break
			}
			if err != nil {
				return nil, err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("source tree ancestor is not a real directory: %s", directory)
			}
		}
		if missing {
			continue
		}
		// OpenRoot also prevents a concurrently replaced file/symlink from
		// escaping this individual allowlisted tree while being read.
		dir, err := os.OpenRoot(directory)
		if err != nil {
			return nil, err
		}
		err = fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if name != "." && privateSourceName(entry.Name()) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !entry.Type().IsRegular() || !sourceExtension(name) {
				return nil
			}
			file, err := dir.Open(name)
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(io.LimitReader(file, maxSourceSize+1))
			closeErr := file.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return err
			}
			if len(data) > maxSourceSize {
				return fmt.Errorf("source exceeds %d bytes: %s/%s", maxSourceSize, tree, name)
			}
			if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
				return fmt.Errorf("non-text source rejected: %s/%s", tree, name)
			}
			total += len(data)
			if total > maxSnapshotSize {
				return fmt.Errorf("UI source snapshot exceeds %d bytes", maxSnapshotSize)
			}
			sources[tree+"/"+name] = string(data)
			return nil
		})
		closeErr := dir.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", tree, err)
		}
	}
	return sources, nil
}

func sourceHandler(sources map[string]string) (http.Handler, error) {
	encoded, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	// Copy the map: the server is a snapshot even if its caller changes theirs.
	static := make(map[string]string, len(sources))
	for name, content := range sources {
		static["/"+name] = content
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body []byte
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			body = []byte(emptyTestDocument)
		case "/__test/sources.json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			body = encoded
		default:
			content, ok := static[r.URL.Path]
			if !ok || path.Clean(r.URL.Path) != r.URL.Path {
				http.NotFound(w, r)
				return
			}
			typeName := mime.TypeByExtension(path.Ext(r.URL.Path))
			if path.Ext(r.URL.Path) == ".js" {
				typeName = "text/javascript; charset=utf-8"
			}
			if typeName == "" || path.Ext(r.URL.Path) == ".go" {
				typeName = "text/plain; charset=utf-8"
			}
			w.Header().Set("Content-Type", typeName)
			body = []byte(content)
		}
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}), nil
}

func serveSnapshot(sources map[string]string) (*httptest.Server, error) {
	handler, err := sourceHandler(sources)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	return server, nil
}
