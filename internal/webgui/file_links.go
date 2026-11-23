package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"supercli/internal/tools/sandbox"
)

// A link in the chat that names a file on this computer (a relative path in
// the project, C:\..., file://...) is resolved here and opened on the
// computer, never fetched as a web page. Only clicks call this; nothing is
// served: the GUI learns whether the file exists and what kind it is, and
// asks for a reveal in the folder or an open. Programs and scripts are never
// started by a click: "open" refuses them, "reveal" selects them in Explorer.

// executableExts are started by the OS when "opened"; a click never does that.
var executableExts = map[string]bool{
	".exe": true, ".com": true, ".bat": true, ".cmd": true, ".ps1": true, ".psm1": true, ".psd1": true,
	".msi": true, ".msp": true, ".mst": true, ".scr": true, ".pif": true, ".cpl": true, ".msc": true,
	".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true, ".wsh": true, ".hta": true,
	".lnk": true, ".url": true, ".reg": true, ".jar": true, ".application": true, ".appref-ms": true,
	".gadget": true, ".scf": true, ".inf": true, ".sh": true, ".py": true, ".pyw": true, ".pyz": true,
	".rb": true, ".pl": true, ".appx": true, ".msix": true, ".appxbundle": true, ".msixbundle": true,
	".dll": true, ".sys": true, ".drv": true, ".ocx": true,
}

// textExts open in SuperCli's own file view (with the line).
var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".log": true, ".json": true, ".jsonl": true, ".toml": true, ".yaml": true, ".yml": true,
	".ini": true, ".cfg": true, ".conf": true, ".xml": true, ".csv": true, ".tsv": true, ".html": true, ".htm": true, ".css": true,
	".go": true, ".zig": true, ".zon": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".rs": true, ".java": true,
	".kt": true, ".cs": true, ".ts": true, ".tsx": true, ".jsx": true, ".js": true, ".mjs": true, ".cjs": true, ".py": true, ".rb": true,
	".pl": true, ".php": true, ".lua": true, ".sh": true, ".ps1": true, ".psm1": true, ".bat": true, ".cmd": true, ".sql": true,
	".gradle": true, ".properties": true, ".mod": true, ".sum": true, ".gitignore": true, ".patch": true, ".diff": true,
	".vue": true, ".svelte": true, ".swift": true, ".dart": true, ".glsl": true, ".hlsl": true, ".vert": true, ".frag": true,
}

func fileLinkKind(path string, isDir bool) (kind string, executable bool) {
	ext := strings.ToLower(filepath.Ext(path))
	executable = !isDir && executableExts[ext]
	switch {
	case isDir:
		return "dir", false
	case textExts[ext] || (ext == "" && strings.HasPrefix(filepath.Base(path), ".")):
		return "text", executable
	case executable:
		return "executable", true
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".svg", ".ppm", ".tga", ".tif", ".tiff":
		return "image", false
	}
	return "other", false
}

var fileLinkLine = regexp.MustCompile(`^(.*?)(?::(\d+))(?::\d+)?$`)

// parseFileLinkTarget turns a link target into a local path and an optional
// line: "src/a.zig:42", "C:\x\a.go", "file:///C:/x/a.go#L9".
func parseFileLinkTarget(target string) (string, int) {
	target = strings.TrimSpace(target)
	line := 0
	if i := strings.Index(target, "#L"); i >= 0 {
		line, _ = strconv.Atoi(strings.TrimPrefix(target[i+2:], ""))
		target = target[:i]
	}
	if strings.HasPrefix(strings.ToLower(target), "file:") {
		if u, err := url.Parse(target); err == nil {
			p := u.Path
			if u.Host != "" && u.Host != "localhost" {
				p = `\\` + u.Host + filepath.FromSlash(p) // UNC
			} else if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
				p = p[1:] // /C:/x -> C:/x
			}
			target = p
		}
	} else if unescaped, err := url.PathUnescape(target); err == nil {
		target = unescaped
	}
	// A drive letter's colon is not a line suffix.
	if m := fileLinkLine.FindStringSubmatch(target); m != nil && !(len(m[1]) == 1 && len(target) > 1 && target[1] == ':') {
		if n, err := strconv.Atoi(m[2]); err == nil && m[1] != "" {
			target, line = m[1], n
		}
	}
	return filepath.Clean(filepath.FromSlash(target)), line
}

type fileLinkInfo struct {
	Exists      bool   `json:"exists"`
	Path        string `json:"path"`
	Kind        string `json:"kind,omitempty"`
	Executable  bool   `json:"executable,omitempty"`
	InWorkspace bool   `json:"in_workspace,omitempty"`
	Line        int    `json:"line,omitempty"`
}

// resolveFileLink resolves a link target against the session's project:
// relative paths only inside it; absolute paths must exist (the same rule as
// the existing "open path" action).
func (s *Server) resolveFileLink(target string) (fileLinkInfo, error) {
	path, line := parseFileLinkTarget(target)
	if path == "" || path == "." {
		return fileLinkInfo{}, fmt.Errorf("missing path")
	}
	home := s.eng.Home()
	var abs string
	if filepath.IsAbs(path) {
		abs = path
	} else {
		inside, err := sandbox.ResolveWithin(home, path)
		if err != nil {
			return fileLinkInfo{}, fmt.Errorf("the path leaves the project")
		}
		abs = inside
	}
	info := fileLinkInfo{Path: abs, Line: line}
	if homeAbs, err := filepath.Abs(home); err == nil {
		info.InWorkspace = underPath(homeAbs, abs)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return info, nil
	}
	info.Exists = true
	info.Kind, info.Executable = fileLinkKind(abs, st.IsDir())
	return info, nil
}

// handleFileLink: POST {target} -> what the link points at;
// POST {target, action: "reveal"|"open"} -> do it.
func (s *Server) handleFileLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Target string `json:"target"`
		Action string `json:"action"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	info, err := s.resolveFileLink(body.Target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if body.Action == "" || !info.Exists {
		writeJSON(w, info)
		return
	}
	switch body.Action {
	case "reveal":
		err = revealInFolder(info.Path, info.Kind == "dir")
	case "open":
		switch {
		case info.Kind == "dir":
			err = openSystemFolder(info.Path)
		case info.Executable:
			http.Error(w, "programs and scripts are not started from a link; use reveal", http.StatusForbidden)
			return
		default:
			err = openSystemFile(info.Path)
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": info.Path})
}

// revealCommand selects a file in the OS file manager (a folder opens).
var revealCommand = func(path string, isDir bool) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		if isDir {
			return exec.Command("explorer.exe", path)
		}
		return exec.Command("explorer.exe", "/select,"+path)
	case "darwin":
		if isDir {
			return exec.Command("open", path)
		}
		return exec.Command("open", "-R", path)
	default:
		if !isDir {
			path = filepath.Dir(path)
		}
		return exec.Command("xdg-open", path)
	}
}

func revealInFolder(path string, isDir bool) error {
	cmd := revealCommand(path, isDir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("show in folder: %w", err)
	}
	return cmd.Process.Release()
}
