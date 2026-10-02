package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"supercli/internal/system/childproc"
)

// captureClipboardWindows uses PowerShell +
// System.Windows.Forms.Clipboard to read the
// current clipboard image, re-encode it as
// PNG, and emit the base64 bytes to stdout.
// Returns ("image/png", bytes) on success.
//
// We shell out instead of calling Win32
// directly so the tool stays in pure Go —
// no cgo, no syscall imports, no platform-
// specific code paths in the hot loop.
func captureClipboardWindows(ctx context.Context) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	// PowerShell one-liner. Stderr is captured
	// so the user sees why a capture failed
	// (e.g. "Clipboard is empty" vs. "no
	// image on clipboard").
	const script = `
Add-Type -AssemblyName System.Windows.Forms;
Add-Type -AssemblyName System.Drawing;
$img = [System.Windows.Forms.Clipboard]::GetImage();
if ($null -eq $img) {
  [Console]::Error.WriteLine('clipboard holds no image')
  exit 1
}
$ms = New-Object System.IO.MemoryStream
$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
[System.Convert]::ToBase64String($ms.ToArray())
`
	cmd := exec.CommandContext(ctx, "powershell", "-STA", "-NoProfile", "-NonInteractive", "-Command", script)
	childproc.HideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &captureBuffer{buffer: &stdout, remaining: DefaultMaxScreenshotBytes * 2, ctx: ctx}
	cmd.Stderr = &captureBuffer{buffer: &stderr, remaining: 4096, ctx: ctx}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("powershell: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	b64 := strings.TrimSpace(stdout.String())
	if b64 == "" {
		return nil, "", fmt.Errorf("powershell returned empty output")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", fmt.Errorf("decode base64: %w", err)
	}
	return raw, "image/png", nil
}

// captureClipboardDarwin reads the clipboard PNG descriptor in source form.
func captureClipboardDarwin(ctx context.Context) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	// Source-form AppleScript serializes its data descriptor as
	// «data PNGf<hex>». Do not reinterpret that descriptor as an integer list.
	const script = `get the clipboard as «class PNGf»`
	cmd := exec.CommandContext(ctx, "osascript", "-s", "s", "-e", script)
	childproc.HideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &captureBuffer{buffer: &stdout, remaining: DefaultMaxScreenshotBytes * 2, ctx: ctx}
	cmd.Stderr = &captureBuffer{buffer: &stderr, remaining: 4096, ctx: ctx}
	err := cmd.Run()
	out := stdout.Bytes()
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("osascript: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	raw, err := decodeDarwinClipboardPNG(string(out))
	if err != nil {
		return nil, "", err
	}
	return raw, "image/png", nil
}

func decodeDarwinClipboardPNG(output string) ([]byte, error) {
	s := strings.TrimSpace(output)
	const prefix = "«data PNGf"
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, "»") {
		return nil, fmt.Errorf("osascript did not return a PNG data descriptor")
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, prefix), "»")
	if len(s) > DefaultMaxScreenshotBytes*2 {
		return nil, fmt.Errorf("clipboard PNG exceeds limit")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode clipboard PNG: %w", err)
	}
	if sniffMediaType(raw) != "image/png" {
		return nil, fmt.Errorf("clipboard descriptor is not PNG")
	}
	return raw, nil
}

// captureClipboardLinux tries xclip first
// (X11), then wl-paste (Wayland). Both write
// raw image bytes to stdout when the format
// is image/png.
func captureClipboardLinux(ctx context.Context) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotCaptureTimeout)
	defer cancel()
	return captureClipboardLinuxWith(ctx, exec.LookPath, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return captureCommand(ctx, exec.CommandContext(ctx, name, args...), DefaultMaxScreenshotBytes)
	})
}

func captureClipboardLinuxWith(ctx context.Context, lookPath func(string) (string, error), run func(context.Context, string, ...string) ([]byte, error)) ([]byte, string, error) {
	var diagnostics []string
	for _, helper := range []struct {
		name string
		args []string
	}{
		{"xclip", []string{"-selection", "clipboard", "-t", "image/png", "-o"}},
		{"wl-paste", []string{"--type", "image/png"}},
	} {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		path, err := lookPath(helper.name)
		if err != nil {
			continue
		}
		out, err := run(ctx, path, helper.args...)
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if err == nil && len(out) > 0 {
			return out, "image/png", nil
		}
		if err == nil {
			err = fmt.Errorf("clipboard holds no image")
		}
		diagnostics = append(diagnostics, helper.name+": "+err.Error())
	}
	if len(diagnostics) > 0 {
		return nil, "", fmt.Errorf("clipboard image unavailable: %s", strings.Join(diagnostics, "; "))
	}
	return nil, "", fmt.Errorf("send_screenshot: no clipboard tool available (install xclip or wl-paste)")
}
