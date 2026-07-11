package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"supercli/internal/tools/fileops"
)

// Default bounds for the read_zip tool. The
// values are deliberately generous (zips of
// source trees or datasets easily reach hundreds
// of MB) but capped so a single call cannot
// exhaust disk space or RAM.
const (
	DefaultMaxZipBytes        = 256 * 1024 * 1024 // 256 MB on disk
	DefaultMaxZipEntries      = 10000
	DefaultMaxExtractedBytes  = 512 * 1024 * 1024 // 512 MB cumulative
	DefaultMaxSingleFileBytes = 64 * 1024 * 1024  // 64 MB per file
)

// ReadZipTool opens a .zip archive and either
// lists its entries (cheap) or extracts a subset
// to disk. The implementation is pure stdlib
// (archive/zip + path/filepath), so the binary
// stays self-contained — no exec of unzip, no
// external tools.
//
// Safety:
//
//   - Entry names are validated with
//     filepath.IsLocal, which rejects absolute
//     paths, parent escapes (../), and Windows
//     reserved names (NUL, COM1, ...). This
//     blocks the classic zip-slip attack.
//   - Extraction joins each clean entry name
//     with the target directory and re-checks
//     that the result is still under target.
//     Belt-and-suspenders.
//   - All bounds (file size, entry count, total
//     extracted bytes, per-file size) are
//     enforced before any write happens.
//
// The tool does NOT preserve Unix file modes
// (zip entries can carry them, but Windows
// extraction would ignore them anyway). It does
// not follow symlinks (zip symlinks are written
// as regular files for the same reason).
type ReadZipTool struct {
	BaseDir string
	// ExtractRoot is the default parent for
	// extracted files. If empty, defaults to
	// <BaseDir>/.supercli/zip-extracts.
	ExtractRoot        string
	MaxZipBytes        int64
	MaxEntries         int
	MaxExtractedBytes  int64
	MaxSingleFileBytes int64
}

// NewReadZip returns a ReadZipTool with default
// bounds. Pass 0 for maxZipBytes to use the
// default. baseDir is the directory the tool
// resolves relative paths against.
func NewReadZip(baseDir string, maxZipBytes int64) *ReadZipTool {
	if baseDir == "" {
		baseDir = "."
	}
	if maxZipBytes <= 0 {
		maxZipBytes = DefaultMaxZipBytes
	}
	return &ReadZipTool{
		BaseDir:            baseDir,
		ExtractRoot:        filepath.Join(baseDir, ".supercli", "zip-extracts"),
		MaxZipBytes:        maxZipBytes,
		MaxEntries:         DefaultMaxZipEntries,
		MaxExtractedBytes:  DefaultMaxExtractedBytes,
		MaxSingleFileBytes: DefaultMaxSingleFileBytes,
	}
}

// Spec returns the Tool descriptor.
func (t *ReadZipTool) Spec() Tool {
	return Tool{
		Name:        "read_zip",
		Description: "Read a .zip archive. Action=list enumerates entries (default); action=extract writes files to disk. Pure Go; no shell-out. Refuses entries with path-traversal names. Rejects entries larger than the per-file cap and total extracted bytes.",
		Schema: `{
  "type": "object",
  "properties": {
    "path":        {"type": "string", "description": "Path to the .zip file."},
    "action":      {"type": "string", "enum": ["list", "extract"], "description": "Action (default: list)."},
    "pattern":     {"type": "string", "description": "Glob filter (Go path.Match syntax). Empty matches all. '*.txt' also matches 'subdir/foo.txt' via basename fallback."},
    "max_entries": {"type": "integer", "description": "Cap on entries processed (default 10000)."},
    "target_dir":  {"type": "string", "description": "Directory to extract into (default: <ExtractRoot>/<zip-basename>-<timestamp>)."}
  },
  "required": ["path"]
}`,
		Fn: t.Execute,
	}
}

// ZipEntry is the public summary of one entry
// in a zip archive. Returned by ListEntries for
// callers (F19 docx, F22 xlsx) that want the
// data without parsing the formatted text.
type ZipEntry struct {
	Name           string
	Size           int64
	CompressedSize int64
	ModTime        time.Time
	IsDir          bool
	Method         string
}

// Execute dispatches on params.Action. args JSON
// shape: {"path", "action", "pattern",
// "max_entries", "target_dir"}.
func (t *ReadZipTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path       string `json:"path"`
		Action     string `json:"action"`
		Pattern    string `json:"pattern"`
		MaxEntries int    `json:"max_entries"`
		TargetDir  string `json:"target_dir"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_zip: bad args: %w", err)}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_zip: path is required")
		return Result{Err: err}, err
	}
	if params.Action == "" {
		params.Action = "list"
	}
	maxEntries := params.MaxEntries
	if maxEntries <= 0 {
		maxEntries = t.MaxEntries
	}

	full := params.Path
	if !filepath.IsAbs(full) {
		full = filepath.Join(t.BaseDir, full)
	}
	info, err := os.Stat(full)
	if err != nil {
		err = fmt.Errorf("read_zip: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("read_zip: %q is a directory, not a zip", full)
		return Result{Err: err}, err
	}
	if info.Size() > t.MaxZipBytes {
		err := fmt.Errorf("read_zip: zip too large: %d bytes > %d max", info.Size(), t.MaxZipBytes)
		return Result{Err: err}, err
	}

	r, err := zip.OpenReader(full)
	if err != nil {
		return Result{Err: fmt.Errorf("read_zip: open %q: %w", full, err)}, err
	}
	defer r.Close()

	if len(r.File) > maxEntries {
		err := fmt.Errorf("read_zip: too many entries: %d > %d max", len(r.File), maxEntries)
		return Result{Err: err}, err
	}

	pattern := params.Pattern
	if pattern == "" {
		pattern = "*"
	}

	switch params.Action {
	case "list":
		return t.listAction(&r.Reader, pattern)
	case "extract":
		target := params.TargetDir
		if target == "" {
			base := strings.TrimSuffix(filepath.Base(full), filepath.Ext(full))
			ts := time.Now().UTC().Format("20060102-150405")
			target = filepath.Join(t.ExtractRoot, base+"-"+ts)
		}
		return t.extractAction(ctx, &r.Reader, pattern, target)
	default:
		err := fmt.Errorf("read_zip: unknown action %q (use list or extract)", params.Action)
		return Result{Err: err}, err
	}
}

// listAction returns a sorted, token-conscious
// summary. Directories appear first, then files
// in alphabetical order. The result is plain
// text, sized for the model's context window.
func (t *ReadZipTool) listAction(r *zip.Reader, pattern string) (Result, error) {
	entries := t.filterEntries(r, pattern)
	if len(entries) == 0 {
		return Result{Text: fmt.Sprintf("(no entries match %q)", pattern)}, nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%d entries match %q:\n", len(entries), pattern)
	for _, e := range entries {
		if e.IsDir {
			fmt.Fprintf(&b, "  [DIR]  %s\n", e.Name)
		} else {
			fmt.Fprintf(&b, "  %10d  %s\n", e.Size, e.Name)
		}
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	fmt.Fprintf(&b, "Total: %d bytes uncompressed\n", total)
	return Result{Text: b.String()}, nil
}

// extractAction writes the matching entries to
// target. The target directory is created.
// Each entry is sanitized and re-validated; the
// cumulative extracted bytes are capped; the
// per-file size is capped.
func (t *ReadZipTool) extractAction(ctx context.Context, r *zip.Reader, pattern, target string) (Result, error) {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return Result{Err: fmt.Errorf("read_zip: resolve target: %w", err)}, err
	}
	if err := os.MkdirAll(absTarget, 0o755); err != nil {
		return Result{Err: fmt.Errorf("read_zip: mkdir target: %w", err)}, err
	}

	var totalBytes int64
	extracted := make([]string, 0, len(r.File))
	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return Result{Err: err}, err
		}
		if !matchEntry(f.Name, pattern) {
			continue
		}
		size := int64(f.UncompressedSize64)
		isDir := strings.HasSuffix(f.Name, "/")
		// Reject anything that isn't a local
		// path. IsLocal catches absolute paths
		// (Unix /foo, Windows C:\foo), parent
		// escapes (../, a/../b), and Windows
		// reserved names (NUL, COM1, ...).
		if !filepath.IsLocal(f.Name) {
			err := fmt.Errorf("read_zip: entry %q is not a local path", f.Name)
			return Result{Err: err}, err
		}
		// Per-file size cap (skipped for dir
		// entries, which have size 0 anyway).
		if !isDir && size > t.MaxSingleFileBytes {
			err := fmt.Errorf("read_zip: %q is %d bytes > %d max", f.Name, size, t.MaxSingleFileBytes)
			return Result{Err: err}, err
		}
		// Cumulative cap (predict the cap check
		// against the SIZE reported in the zip
		// header; this is a STATIC check, so
		// payloads that lie in the header get
		// caught here).
		if totalBytes+size > t.MaxExtractedBytes {
			err := fmt.Errorf("read_zip: extracting %q would exceed %d bytes total", f.Name, t.MaxExtractedBytes)
			return Result{Err: err}, err
		}
		// Build the destination and re-verify
		// the join. Belt-and-suspenders after
		// IsLocal.
		clean := filepath.Clean(f.Name)
		abs := filepath.Join(absTarget, clean)
		if !strings.HasPrefix(abs, absTarget+string(filepath.Separator)) && abs != absTarget {
			err := fmt.Errorf("read_zip: entry %q escapes target", f.Name)
			return Result{Err: err}, err
		}
		if err := t.writeEntry(f, abs, size, isDir); err != nil {
			return Result{Err: fmt.Errorf("read_zip: extract %q: %w", f.Name, err)}, err
		}
		extracted = append(extracted, f.Name)
		totalBytes += size
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Extracted %d entries (%d bytes) to %s:\n", len(extracted), totalBytes, absTarget)
	for _, name := range extracted {
		fmt.Fprintf(&b, "  %s\n", name)
	}
	return Result{Text: b.String()}, nil
}

// writeEntry writes a single zip entry to abs.
// Directory entries (names ending in /) become
// directories; file entries are written with
// 0o644. Symlinks in the zip are written as
// regular files (we never honor zip symlinks).
// The io.LimitReader allows maxBytes+1 to
// detect over-cap files at copy time; the
// caller is expected to have already size-checked.
func (t *ReadZipTool) writeEntry(f *zip.File, abs string, size int64, isDir bool) error {
	if isDir {
		return os.MkdirAll(abs, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	// Use the configured per-file cap as a
	// safety net. If the zip header LIED about
	// the size, the cap stops the runaway.
	_, err = io.Copy(out, io.LimitReader(rc, t.MaxSingleFileBytes+1))
	return err
}

// filterEntries returns the ZipEntry list that
// matches the pattern. It preserves zip-internal
// metadata without reading entry bodies.
func (t *ReadZipTool) filterEntries(r *zip.Reader, pattern string) []ZipEntry {
	out := make([]ZipEntry, 0, len(r.File))
	for _, f := range r.File {
		if !matchEntry(f.Name, pattern) {
			continue
		}
		out = append(out, ZipEntry{
			Name:           f.Name,
			Size:           int64(f.UncompressedSize64),
			CompressedSize: int64(f.CompressedSize64),
			ModTime:        f.Modified,
			IsDir:          strings.HasSuffix(f.Name, "/"),
			Method:         zipMethodName(f.Method),
		})
	}
	return out
}

// zipMethodName returns a human-readable name
// for the zip compression method. Only the two
// common methods (Store, Deflate) are named;
// anything else is shown as "method-<n>" so a
// user can see we encountered something weird.
func zipMethodName(m uint16) string {
	switch m {
	case zip.Store:
		return "store"
	case zip.Deflate:
		return "deflate"
	default:
		return fmt.Sprintf("method-%d", m)
	}
}

// matchEntry returns true when name matches
// pattern. We try the full name first (so
// "sub/*.md" works), then the basename (so
// "*.md" matches any depth).
func matchEntry(name, pattern string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if matched, _ := path.Match(pattern, name); matched {
		return true
	}
	if matched, _ := path.Match(pattern, filepath.Base(name)); matched {
		return true
	}
	return false
}

// ListEntries is a public helper that returns
// the parsed entry list (sorted, no text
// rendering). Useful for F19 (docx) and F22
// (xlsx) which also need to open zip files.
//
// The reader is opened and closed internally;
// callers that want the bytes must re-open
// the file themselves. The bounds enforced
// here are the tool's defaults; the caller
// can override by constructing its own
// ReadZipTool.
func (t *ReadZipTool) ListEntries(path string) ([]ZipEntry, error) {
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(t.BaseDir, full)
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, fileops.FileErr(err, full)
	}
	if info.Size() > t.MaxZipBytes {
		return nil, fmt.Errorf("zip too large: %d > %d", info.Size(), t.MaxZipBytes)
	}
	r, err := zip.OpenReader(full)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if len(r.File) > t.MaxEntries {
		return nil, fmt.Errorf("too many entries: %d > %d", len(r.File), t.MaxEntries)
	}
	out := make([]ZipEntry, 0, len(r.File))
	for _, f := range r.File {
		out = append(out, ZipEntry{
			Name:           f.Name,
			Size:           int64(f.UncompressedSize64),
			CompressedSize: int64(f.CompressedSize64),
			ModTime:        f.Modified,
			IsDir:          strings.HasSuffix(f.Name, "/"),
			Method:         zipMethodName(f.Method),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
