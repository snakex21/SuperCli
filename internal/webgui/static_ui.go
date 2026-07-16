package webgui

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// UseUIRoot replaces the embedded front-end while preserving every API route.
// It must be called before Handler.
func (s *Server) UseUIRoot(root string) error {
	handler, err := staticUIHandler(root)
	if err != nil {
		return err
	}
	s.uiHandler = handler
	return nil
}

// UseUIFS replaces the embedded SuperCli front-end with another embedded
// filesystem. It lets branded builds remain a single executable.
func (s *Server) UseUIFS(fsys fs.FS) error {
	handler, err := staticUIFSHandler(fsys)
	if err != nil {
		return err
	}
	s.uiHandler = handler
	return nil
}

func staticUIHandler(root string) (http.Handler, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("webgui: ui root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("webgui: resolve ui root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("webgui: ui root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("webgui: ui root is not a directory: %s", abs)
	}
	index := filepath.Join(abs, "index.html")
	indexInfo, err := os.Stat(index)
	if err != nil {
		return nil, fmt.Errorf("webgui: ui root requires index.html: %w", err)
	}
	if indexInfo.IsDir() {
		return nil, fmt.Errorf("webgui: ui root index.html is not a file: %s", index)
	}
	return http.FileServer(http.Dir(abs)), nil
}

func staticUIFSHandler(fsys fs.FS) (http.Handler, error) {
	if fsys == nil {
		return nil, fmt.Errorf("webgui: UI filesystem is nil")
	}
	info, err := fs.Stat(fsys, "index.html")
	if err != nil {
		return nil, fmt.Errorf("webgui: embedded UI requires index.html: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("webgui: embedded UI index.html is not a file")
	}
	return http.FileServer(http.FS(fsys)), nil
}
