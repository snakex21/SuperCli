package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// Receipts are run-local trusted evidence, not a global URL cache or a grant to
// overwrite files. Signed URLs are represented only by a digest of the request.
type downloadReceipt struct {
	request [sha256.Size]byte
	path    string
	size    int64
	digest  [sha256.Size]byte
	info    os.FileInfo
}

func downloadReceiptKey(url, path string, limit int64) [sha256.Size]byte {
	encoded, _ := json.Marshal(webDownloadArgs{URL: url, Path: path, MaxBytes: limit})
	return sha256.Sum256(encoded)
}

// replaySuccess checks this tool's completed effect without contacting its
// source again. A new user request is a new operation; the loop owns receipt
// lifetime and only invokes this callback for an identical prepared request.
func (t *WebDownload) replaySuccess(ctx context.Context, raw json.RawMessage, prior Result) (Result, bool) {
	failure := func(err error) (Result, bool) {
		return Result{Err: fmt.Errorf("web_download: %w", err)}, true
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	if prior.Err != nil || prior.Operation == nil {
		return Result{}, false
	}
	receipt, ok := prior.Operation.Evidence.(*downloadReceipt)
	if !ok || receipt == nil || receipt.info == nil || receipt.size < 1 || receipt.size > webDownloadMaxBytes {
		return Result{}, false
	}
	var args webDownloadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return failure(fmt.Errorf("bad args: %w", err))
	}
	u, err := validateFetchURL(args.URL)
	if err != nil || u.User != nil {
		return failure(fmt.Errorf("require a public HTTP(S) URL without username/password"))
	}
	if strings.TrimSpace(args.Path) == "" {
		return failure(fmt.Errorf("path is required"))
	}
	limit := args.MaxBytes
	if limit == 0 {
		limit = webDownloadDefaultBytes
	}
	if limit < 1 || limit > webDownloadMaxBytes {
		return failure(fmt.Errorf("max_bytes must be between 1 and %d", webDownloadMaxBytes))
	}
	full, err := sandbox.ResolveDownloadDestination(ctx, t.BaseDir, args.Path)
	if err != nil {
		return failure(err)
	}
	if full != receipt.path {
		return failure(fmt.Errorf("completed download destination changed; no HTTP request made"))
	}
	if receipt.request != downloadReceiptKey(u.String(), full, limit) || receipt.size > limit {
		return Result{}, false
	}
	release, err := fileops.LockMutationPathsContext(ctx, full)
	if err != nil {
		return failure(err)
	}
	defer release()
	before, err := os.Lstat(full)
	if os.IsNotExist(err) {
		// The effect is gone. Normal execution may create the missing file under
		// the same current permission; an absent file can never be replayed.
		return Result{}, false
	}
	if err != nil {
		return failure(err)
	}
	mismatch := fmt.Errorf("completed download no longer matches its verified receipt; existing file preserved, no HTTP request made")
	if !before.Mode().IsRegular() || before.Size() != receipt.size {
		return failure(mismatch)
	}
	file, err := os.Open(full)
	if err != nil {
		return failure(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return failure(err)
	}
	// Compare the opened native identity before reading. A replaced file or
	// raced symlink is not this receipt, even when its size/mtime look identical.
	if !opened.Mode().IsRegular() || opened.Size() != receipt.size ||
		!os.SameFile(receipt.info, opened) || !os.SameFile(before, opened) {
		return failure(mismatch)
	}
	if checked, err := sandbox.ResolveDownloadDestination(ctx, t.BaseDir, args.Path); err != nil || checked != full {
		if err != nil {
			return failure(err)
		}
		return failure(mismatch)
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, io.LimitReader(&downloadContextReader{ctx: ctx, reader: file}, receipt.size+1), make([]byte, 32<<10))
	if err != nil {
		return failure(err)
	}
	after, err := file.Stat()
	if err != nil {
		return failure(err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	current, err := os.Lstat(full)
	if err != nil {
		return failure(err)
	}
	if n != receipt.size || digest != receipt.digest || !after.Mode().IsRegular() ||
		after.Size() != receipt.size || !after.ModTime().Equal(opened.ModTime()) ||
		!current.Mode().IsRegular() || current.Size() != receipt.size ||
		!current.ModTime().Equal(after.ModTime()) || !os.SameFile(opened, after) || !os.SameFile(after, current) {
		return failure(mismatch)
	}
	if checked, err := sandbox.ResolveDownloadDestination(ctx, t.BaseDir, args.Path); err != nil || checked != full {
		if err != nil {
			return failure(err)
		}
		return failure(mismatch)
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	return Result{
		Text:  fmt.Sprintf("Download already complete and verified: %q (%d bytes)\nSHA256: %x\nNo HTTP request made; existing file preserved. Continue only remaining requirements.", full, receipt.size, receipt.digest),
		Inert: true, Operation: prior.Operation,
	}, true
}

// Advice is conditional, derived from the already-read response header, and
// never opens the saved asset again. A filename/server MIME alone is not enough
// to call a body binary: SVG/source files and mislabeled text remain readable.
func downloadInspectionHint(path, sniffedType string, binaryPrefix bool, size int64) string {
	reader := ""
	switch sniffedType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		// read_image defaults to a 10 MiB file cap; larger assets are still
		// usable, without advising a reader that will reject their size.
		if size <= 10<<20 {
			reader = "read_image"
		}
	case "application/pdf":
		reader = "read_pdf"
	case "application/zip":
		switch strings.ToLower(filepath.Ext(path)) {
		case ".docx":
			if size <= 64<<20 {
				reader = "read_docx"
			}
		case ".xlsx":
			if size <= 64<<20 {
				reader = "read_xlsx"
			}
		default:
			reader = "read_zip"
		}
	}
	if reader != "" {
		return "Inspect only if needed with " + reader + "; binary data is not text lines."
	}
	if !strings.HasPrefix(sniffedType, "text/") && binaryPrefix {
		return "Binary asset; inspect with a format-aware tool only if needed."
	}
	return ""
}
