// file_ops.go implements the file_ops tool: safe, office-user
// oriented file management. Design rules:
//
//   - All paths go through the sandbox (sandbox.ResolveSafe):
//     they resolve against the working directory and may not
//     escape it or touch sensitive system roots.
//   - NOTHING is ever silently overwritten. If a destination
//     exists, the tool returns an error instructing the model
//     to ask the user.
//   - There is NO hard-delete action. "trash" moves the file
//     into <workdir>/.supercli/trash/ with a timestamp prefix
//     (NOT the Windows Recycle Bin — that needs shell32 COM
//     calls we avoid to stay pure Go and portable). The
//     trashed file can be restored by moving it back.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileOpsTool performs safe file management operations.
type FileOpsTool struct {
	BaseDir string
	// Now is injectable for tests (timestamp prefix of
	// trashed files).
	Now func() time.Time
	// MaxListEntries caps list output.
	MaxListEntries int
}

// NewFileOps returns a FileOpsTool rooted at baseDir.
func NewFileOps(baseDir string) *FileOpsTool {
	if baseDir == "" {
		baseDir = "."
	}
	return &FileOpsTool{BaseDir: baseDir, Now: time.Now, MaxListEntries: 500}
}

// Spec returns the Tool descriptor.
func (t *FileOpsTool) Spec() Tool {
	return Tool{
		Name: "file_ops",
		Description: "Safely manage files and folders: move, copy, rename, create folders, list contents, " +
			"and send files to a trash folder. Use this for organizing the user's documents — e.g. sorting " +
			"files into folders, renaming reports, archiving old drafts. " +
			"Actions: 'move' (move a file/folder; if the destination is an existing folder the item moves INTO it), " +
			"'copy' (copy a file or a whole folder), 'rename' (same as move), 'create_folder', " +
			"'list' (show a folder's contents; set recursive=true to include subfolders), " +
			"'trash' (move a file/folder into the .supercli/trash folder with a timestamp — NOT the Windows " +
			"Recycle Bin; it can be undone by moving the item back out). " +
			"Safety: it NEVER overwrites — if a destination already exists you get an error; ask the user how " +
			"to proceed. There is no permanent-delete action at all; use 'trash' and tell the user where the " +
			"item went. Paths must stay inside the working directory. " +
			"Do NOT use this to edit file contents (use the edit tools) or to run programs.",
		Schema: `{
  "type": "object",
  "properties": {
    "action":    {"type": "string", "enum": ["move", "copy", "rename", "create_folder", "list", "trash"], "description": "What to do."},
    "path":      {"type": "string", "description": "The source file or folder."},
    "dest":      {"type": "string", "description": "move/copy/rename: the destination path (or an existing folder to move/copy into)."},
    "recursive": {"type": "boolean", "description": "list: include subfolders (default false)."}
  },
  "required": ["action", "path"]
}`,
		Fn: t.Execute,
	}
}

type fileOpsArgs struct {
	Action    string `json:"action"`
	Path      string `json:"path"`
	Dest      string `json:"dest"`
	Recursive bool   `json:"recursive"`
}

// Execute dispatches on action.
func (t *FileOpsTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var p fileOpsArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("file_ops: bad args: %w", err)}, err
	}
	src, err := resolveSandboxed(t.BaseDir, p.Path)
	if err != nil {
		err = fmt.Errorf("file_ops: %w", err)
		return Result{Err: err}, err
	}
	switch p.Action {
	case "move", "rename":
		return t.doMove(src, p)
	case "copy":
		return t.doCopy(src, p)
	case "create_folder":
		return t.doCreateFolder(src)
	case "list":
		return t.doList(src, p.Recursive)
	case "trash":
		return t.doTrash(src)
	default:
		err := fmt.Errorf("file_ops: unknown action %q (want move|copy|rename|create_folder|list|trash)", p.Action)
		return Result{Err: err}, err
	}
}

// resolveDest validates the destination and applies the
// move-INTO-folder convenience: when dest is an existing
// directory and src is not the same path, the final dest is
// dest/<basename(src)>. The returned path is guaranteed not
// to exist yet (no-overwrite rule).
func (t *FileOpsTool) resolveDest(src string, p fileOpsArgs) (string, error) {
	if p.Dest == "" {
		return "", fmt.Errorf("'dest' is required for %s", p.Action)
	}
	dst, err := resolveSandboxed(t.BaseDir, p.Dest)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(dst); err == nil {
		if info.IsDir() && dst != src {
			dst = filepath.Join(dst, filepath.Base(src))
		}
		if _, err := os.Lstat(dst); err == nil {
			return "", fmt.Errorf("destination %q already exists. Refusing to overwrite — ask the user whether to replace it, keep both (different name), or cancel", dst)
		}
	}
	return dst, nil
}

func (t *FileOpsTool) doMove(src string, p fileOpsArgs) (Result, error) {
	if _, err := os.Lstat(src); err != nil {
		return Result{Err: fmt.Errorf("file_ops: source %q: %w", src, err)}, err
	}
	dst, err := t.resolveDest(src, p)
	if err != nil {
		err = fmt.Errorf("file_ops: %w", err)
		return Result{Err: err}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Result{Err: fmt.Errorf("file_ops: %w", err)}, err
	}
	if err := os.Rename(src, dst); err != nil {
		return Result{Err: fmt.Errorf("file_ops: move: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Moved %s -> %s", src, dst)}, nil
}

func (t *FileOpsTool) doCopy(src string, p fileOpsArgs) (Result, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return Result{Err: fmt.Errorf("file_ops: source %q: %w", src, err)}, err
	}
	dst, err := t.resolveDest(src, p)
	if err != nil {
		err = fmt.Errorf("file_ops: %w", err)
		return Result{Err: err}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Result{Err: fmt.Errorf("file_ops: %w", err)}, err
	}
	if info.IsDir() {
		if err := copyDirRecursive(src, dst); err != nil {
			return Result{Err: fmt.Errorf("file_ops: copy folder: %w", err)}, err
		}
	} else {
		if err := copyFile(src, dst); err != nil {
			return Result{Err: fmt.Errorf("file_ops: copy: %w", err)}, err
		}
	}
	return Result{Text: fmt.Sprintf("Copied %s -> %s", src, dst)}, nil
}

func (t *FileOpsTool) doCreateFolder(path string) (Result, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return Result{Text: fmt.Sprintf("Folder %s already exists (nothing changed).", path)}, nil
		}
		err := fmt.Errorf("file_ops: %q already exists and is a file, not a folder", path)
		return Result{Err: err}, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return Result{Err: fmt.Errorf("file_ops: create folder: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Created folder %s", path)}, nil
}

func (t *FileOpsTool) doList(path string, recursive bool) (Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Result{Err: fmt.Errorf("file_ops: %w", err)}, err
	}
	if !info.IsDir() {
		return Result{Text: fmt.Sprintf("%s (file, %d bytes)", path, info.Size())}, nil
	}
	var lines []string
	truncated := false
	add := func(p string, d fs.DirEntry) {
		rel, err := filepath.Rel(path, p)
		if err != nil {
			rel = p
		}
		if d.IsDir() {
			lines = append(lines, rel+string(filepath.Separator))
			return
		}
		fi, err := d.Info()
		size := int64(0)
		if err == nil {
			size = fi.Size()
		}
		lines = append(lines, fmt.Sprintf("%s (%d bytes)", rel, size))
	}
	if recursive {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if p == path {
				return nil
			}
			if len(lines) >= t.MaxListEntries {
				truncated = true
				return fs.SkipAll
			}
			add(p, d)
			return nil
		})
		if err != nil {
			return Result{Err: fmt.Errorf("file_ops: list: %w", err)}, err
		}
	} else {
		entries, err := os.ReadDir(path)
		if err != nil {
			return Result{Err: fmt.Errorf("file_ops: list: %w", err)}, err
		}
		for _, d := range entries {
			if len(lines) >= t.MaxListEntries {
				truncated = true
				break
			}
			add(filepath.Join(path, d.Name()), d)
		}
	}
	sort.Strings(lines)
	out := fmt.Sprintf("%s contains %d item(s):\n%s", path, len(lines), strings.Join(lines, "\n"))
	if truncated {
		out += fmt.Sprintf("\n... (truncated at %d entries)", t.MaxListEntries)
	}
	if len(lines) == 0 {
		out = fmt.Sprintf("%s is empty.", path)
	}
	return Result{Text: out}, nil
}

func (t *FileOpsTool) doTrash(src string) (Result, error) {
	if _, err := os.Lstat(src); err != nil {
		return Result{Err: fmt.Errorf("file_ops: source %q: %w", src, err)}, err
	}
	trashDir := filepath.Join(t.BaseDir, ".supercli", "trash")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return Result{Err: fmt.Errorf("file_ops: prepare trash folder: %w", err)}, err
	}
	stamp := t.Now().Format("20060102-150405")
	base := stamp + "_" + filepath.Base(src)
	dst := filepath.Join(trashDir, base)
	for i := 1; ; i++ {
		if _, err := os.Lstat(dst); err != nil {
			break
		}
		dst = filepath.Join(trashDir, fmt.Sprintf("%s-%d_%s", stamp, i, filepath.Base(src)))
	}
	if err := os.Rename(src, dst); err != nil {
		return Result{Err: fmt.Errorf("file_ops: trash: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Moved %s to the trash folder: %s. It can be restored by moving it back.", src, dst)}, nil
}

// copyDirRecursive copies the directory tree at src to dst.
// dst must not exist. Symlinks are skipped (office documents
// don't use them; copying link targets could escape the
// sandbox).
func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}
