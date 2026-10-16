// Package phonetunnel owns the opt-in Cloudflare quick tunnel and its pinned tool.
package phonetunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const Version = "2026.10.1"
const DownloadSize int64 = 55325120
const DownloadURL = "https://github.com/cloudflare/cloudflared/releases/download/2026.10.1/cloudflared-windows-amd64.exe"
const DownloadSHA256 = "4d2490e74082c5d48dbfedaf92576fa5bd1702379ab88eb7faa0cfbe8dc891b6"

var ErrNeedDownload = errors.New("cloudflared download requires consent")

// verifiedFiles remembers files whose SHA256 already matched, keyed by path, so
// status polls and restarts do not re-hash 55 MB while size and mtime are unchanged.
var verifiedFiles sync.Map

type verifiedStamp struct {
	hash      string
	size, mod int64
}

type Progress func(downloaded, total int64)

type Installer struct {
	DataDir string
	// URL, SHA256 and Size can be overridden by offline tests.
	URL, SHA256 string
	Size        int64
	Client      *http.Client
	Platform    string
}

func (i Installer) spec() (string, string, int64, error) {
	platform := i.Platform
	if platform == "" {
		platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	if platform != "windows/amd64" {
		return "", "", 0, fmt.Errorf("unsupported platform / nieobsługiwana platforma: %s", platform)
	}
	u, hash, size := i.URL, i.SHA256, i.Size
	if u == "" {
		u = DownloadURL
	}
	if hash == "" {
		hash = DownloadSHA256
	}
	if size == 0 {
		size = DownloadSize
	}
	return u, hash, size, nil
}

func (i Installer) Path() string {
	return filepath.Join(i.DataDir, "tools", "cloudflared", Version, "cloudflared.exe")
}

func (i Installer) Available() bool {
	_, hash, size, err := i.spec()
	if err != nil {
		return false
	}
	path := i.Path()
	info, err := os.Stat(path)
	if err != nil || info.Size() != size {
		return false
	}
	stamp := verifiedStamp{hash: hash, size: size, mod: info.ModTime().UnixNano()}
	if v, ok := verifiedFiles.Load(path); ok && v.(verifiedStamp) == stamp {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		verifiedFiles.Delete(path)
		return false
	}
	verifiedFiles.Store(path, stamp)
	return true
}

func (i Installer) Ensure(ctx context.Context, consent bool, progress Progress) (string, error) {
	u, hash, size, err := i.spec()
	if err != nil {
		return "", err
	}
	if i.Available() {
		return i.Path(), nil
	}
	if !consent {
		return "", ErrNeedDownload
	}
	if err := os.MkdirAll(filepath.Dir(i.Path()), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(i.Path()), "download-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	client := i.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cloudflared download: HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	writer := &progressWriter{Writer: io.MultiWriter(f, h), progress: progress, total: size}
	n, err := io.Copy(writer, io.LimitReader(resp.Body, size+1))
	if err != nil {
		return "", err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		return "", errors.New("cloudflared SHA256 or size mismatch")
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(name, 0700); err != nil {
		return "", err
	}
	// Windows cannot replace an existing executable using os.Rename.
	if _, err := os.Stat(i.Path()); err == nil {
		if err := os.Remove(i.Path()); err != nil {
			return "", err
		}
	}
	if err := os.Rename(name, i.Path()); err != nil {
		return "", err
	}
	// The bytes were hashed while downloading; remember them to skip a re-hash.
	if info, err := os.Stat(i.Path()); err == nil {
		verifiedFiles.Store(i.Path(), verifiedStamp{hash: hash, size: size, mod: info.ModTime().UnixNano()})
	}
	return i.Path(), nil
}

type progressWriter struct {
	io.Writer
	progress Progress
	n, total int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.n += int64(n)
	if w.progress != nil {
		w.progress(w.n, w.total)
	}
	return n, err
}
