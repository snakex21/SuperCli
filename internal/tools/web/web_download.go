package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

const (
	webDownloadDefaultBytes int64 = 64 << 20
	webDownloadMaxBytes     int64 = 256 << 20
)

// WebDownload streams public assets into a new workspace or requested export file. No
// binary/base64 body is loaded into the model context or a shell command.
type WebDownload struct {
	BaseDir string
	client  *http.Client
}

func NewWebDownload(baseDir string) *WebDownload {
	// The caller owns cancellation of the complete download. Bound connection
	// setup, but allow a healthy streamed body to finish without a runtime cap.
	client := newPublicHTTPClient(0)
	// A forward proxy would move origin DNS resolution outside safeDial and
	// could attach proxy credentials. Public file downloads use direct guarded
	// connections so every origin/redirect address is checked locally.
	transport := client.Transport.(*http.Transport)
	transport.Proxy = nil
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &WebDownload{BaseDir: baseDir, client: client}
}

type webDownloadArgs struct {
	URL      string `json:"url"`
	Path     string `json:"path"`
	MaxBytes int64  `json:"max_bytes"`
}

func (t *WebDownload) Spec() Tool {
	return Tool{
		Name:        "web_download",
		Description: "Download a public HTTP(S) URL directly to a new local file: game assets, textures, images, audio, 3D models, fonts, ZIP archives or documents. Streams bytes to the project folder or an export folder requested by the user (such as Downloads), creates parent folders, and returns only path, byte count, content type and SHA256. Use instead of curl/PowerShell/Python downloads or web_fetch for binary files. Requires a direct file URL; refuses HTML pages and existing destinations. One call waits until complete; no progress polling. Public URLs only; no authentication headers.",
		Schema: `{
		  "type": "object",
		  "properties": {
		    "url": {"type": "string", "description": "Direct public HTTP(S) file URL. Redirects are supported."},
		    "path": {"type": "string", "description": "New file path inside the project folder (e.g. assets/texture.png), or an absolute path inside an export folder explicitly requested by the user for this turn."},
		    "max_bytes": {"type": "integer", "minimum": 1, "maximum": 268435456, "description": "Maximum file size in bytes (default 67108864, hard limit 268435456)."}
		  },
		  "required": ["url", "path"],
		  "additionalProperties": false
		}`,
		Fn:            t.execute,
		ReplaySuccess: t.replaySuccess,
	}
}

func (t *WebDownload) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a webDownloadArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("web_download: bad args: %w", err)}, nil
	}
	// URL parser/client errors may contain signed query strings. Downloads
	// report only a cause and hostname; response/error bodies are never echoed.
	u, err := validateFetchURL(a.URL)
	if err != nil || u.User != nil {
		return Result{Err: fmt.Errorf("web_download: require a public HTTP(S) URL without username/password")}, nil
	}
	if strings.TrimSpace(a.Path) == "" {
		return Result{Err: fmt.Errorf("web_download: path is required")}, nil
	}
	maxBytes := a.MaxBytes
	if maxBytes == 0 {
		maxBytes = webDownloadDefaultBytes
	}
	if maxBytes < 1 || maxBytes > webDownloadMaxBytes {
		return Result{Err: fmt.Errorf("web_download: max_bytes must be between 1 and %d", webDownloadMaxBytes)}, nil
	}
	full, err := sandbox.ResolveDownloadDestination(ctx, t.BaseDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
	}
	release, err := fileops.LockMutationPathsContext(ctx, full)
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
	}
	defer release()
	if _, err := os.Lstat(full); err == nil {
		return Result{Err: fmt.Errorf("web_download: destination_exists; existing file preserved, no HTTP request made. Retrying unchanged arguments cannot replace it; reuse the file or explicitly remove it if replacement is required")}, nil
	} else if !os.IsNotExist(err) {
		return Result{Err: fmt.Errorf("web_download: destination: %w", err)}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: invalid download request")}, nil
	}
	req.Header.Set("User-Agent", "SuperCli/1.0 (web_download tool)")
	resp, err := t.client.Do(req)
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: request_failed cause=%s host=%s", classifyNetErr(err), u.Hostname())}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{Err: fmt.Errorf("web_download: http_failed status=%d host=%s; use a direct accessible file URL", resp.StatusCode, u.Hostname())}, nil
	}
	if resp.ContentLength > maxBytes {
		return Result{Err: fmt.Errorf("web_download: file exceeds max_bytes=%d (content_length=%d)", maxBytes, resp.ContentLength)}, nil
	}
	reader := bufio.NewReader(io.LimitReader(resp.Body, maxBytes+1))
	prefix, _ := reader.Peek(512)
	binaryPrefix := bytes.IndexByte(prefix, 0) >= 0
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	sniffedType, _, _ := mime.ParseMediaType(http.DetectContentType(prefix))
	if strings.EqualFold(contentType, "text/html") || strings.EqualFold(contentType, "application/xhtml+xml") || sniffedType == "text/html" {
		return Result{Err: fmt.Errorf("web_download: received an HTML page; use web_fetch to find the direct file URL")}, nil
	}
	if contentType == "" {
		contentType = sniffedType
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return Result{Err: fmt.Errorf("web_download: create parent: %w", err)}, nil
	}
	// Recheck symlink ancestors after mkdir before touching the output folder.
	if checked, err := sandbox.ResolveDownloadDestination(ctx, t.BaseDir, a.Path); err != nil || checked != full {
		if err := ctx.Err(); err != nil {
			return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
		}
		return Result{Err: fmt.Errorf("web_download: destination changed during download")}, nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".supercli-download-*.part")
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: create temporary file: %w", err)}, nil
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(&downloadContextReader{ctx: ctx, reader: reader}, maxBytes+1))
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: body read failed cause=%s", classifyNetErr(err))}, nil
	}
	if n > maxBytes {
		return Result{Err: fmt.Errorf("web_download: file exceeds max_bytes=%d", maxBytes)}, nil
	}
	if n == 0 {
		return Result{Err: fmt.Errorf("web_download: empty response; no file saved")}, nil
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return Result{Err: fmt.Errorf("web_download: incomplete response (received=%d expected=%d); no file saved", n, resp.ContentLength)}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
	}
	if err := tmp.Chmod(0o644); err != nil {
		return Result{Err: fmt.Errorf("web_download: file mode: %w", err)}, nil
	}
	if err := tmp.Sync(); err != nil {
		return Result{Err: fmt.Errorf("web_download: flush file: %w", err)}, nil
	}
	// Capture native identity from the existing handle, not by opening the saved
	// file or reading its body again. Rename/link publication keeps that identity.
	info, err := tmp.Stat()
	if err != nil {
		return Result{Err: fmt.Errorf("web_download: file metadata: %w", err)}, nil
	}
	if err := tmp.Close(); err != nil {
		return Result{Err: fmt.Errorf("web_download: close file: %w", err)}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
	}
	// HTTP may outlive a junction/symlink change. Recheck the same pinned
	// destination immediately before publishing, including export grants.
	if checked, err := sandbox.ResolveDownloadDestination(ctx, t.BaseDir, a.Path); err != nil || checked != full {
		if err := ctx.Err(); err != nil {
			return Result{Err: fmt.Errorf("web_download: %w", err)}, nil
		}
		return Result{Err: fmt.Errorf("web_download: destination changed during download")}, nil
	}
	if err := publishDownload(tmp.Name(), full); err != nil {
		return Result{Err: fmt.Errorf("web_download: publish file without overwrite: %w", err)}, nil
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	text := fmt.Sprintf("Download complete: %s (%d bytes)\nContent-Type: %s\nSHA256: %x\nSize and SHA256 measured while saving; file ready to use.", a.Path, n, contentType, digest)
	if hint := downloadInspectionHint(a.Path, sniffedType, binaryPrefix, n); hint != "" {
		text += "\n" + hint
	}
	quotedPath, _ := json.Marshal(full)
	return Result{Text: text, Operation: &core.VerifiedOperation{
		Summary: fmt.Sprintf("Saved %s (%d bytes; SHA256 %x).", quotedPath, n, digest),
		Evidence: &downloadReceipt{
			request: downloadReceiptKey(u.String(), full, maxBytes),
			path:    full,
			size:    n,
			digest:  digest,
			info:    info,
		},
	}}, nil
}

type downloadContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *downloadContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
