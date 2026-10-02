//go:build !windows

package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"supercli/internal/tools/sandbox"
)

// macOS and Linux use an already-installed system capture helper on demand.
// File-based helpers write only inside the supplied portable project directory.
func captureScreen(ctx context.Context, baseDir string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("grim"); err == nil {
			out, err := captureCommand(ctx, exec.CommandContext(ctx, path, "-"), DefaultMaxScreenshotBytes)
			if err == nil {
				return out, "image/png", nil
			}
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
		}
	}
	var helper string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		helper, args = "screencapture", []string{"-x", "-t", "png"}
	case "linux":
		if _, err := exec.LookPath("gnome-screenshot"); err == nil {
			helper, args = "gnome-screenshot", []string{"-f"}
		} else {
			return nil, "", fmt.Errorf("screen capture requires an installed grim (Wayland) or gnome-screenshot; clipboard capture remains available")
		}
	default:
		return nil, "", fmt.Errorf("screen capture unavailable on %s", runtime.GOOS)
	}
	dir, err := sandbox.ResolveWithin(baseDir, filepath.Join(".supercli", "snapshots"))
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", err
	}
	f, err := os.CreateTemp(dir, ".capture-*.png")
	if err != nil {
		return nil, "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, "", err
	}
	defer os.Remove(path)
	_, err = captureCommand(ctx, exec.CommandContext(ctx, helper, append(args, path)...), 4096)
	if err != nil {
		return nil, "", err
	}
	// Read from the opened descriptor, including the size check, without loading
	// an oversized helper result or falling back to a profile/temp directory.
	out, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer out.Close()
	return readCapturedPNG(ctx, out)
}
