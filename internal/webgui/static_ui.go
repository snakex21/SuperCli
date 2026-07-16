package webgui

import (
	"fmt"
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
