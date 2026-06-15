package media

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
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
func captureClipboardWindows() ([]byte, string, error) {
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
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
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

// captureClipboardDarwin uses osascript to
// read the clipboard as PNG. macOS's
// NSPasteboard returns the image as raw
// bytes when fetched via the as «class PNGf»
// coercion. We hex-encode the bytes inside
// osascript because osascript returns a list
// of integers; the only sane way to ferry
// arbitrary binary out of the AppleScript
// bridge is via hex characters.
func captureClipboardDarwin() ([]byte, string, error) {
	const script = `
try
  set theData to the clipboard as «class PNGf»
  set theBytes to theData
  set theText to ""
  repeat with b in theBytes
    set theText to theText & (character ((b mod 16) + 1) of "0123456789abcdef")
    set theText to theText & (character ((b div 16) + 1) of "0123456789abcdef")
  end repeat
  return theText
on error errMsg
  return "ERROR:" & errMsg
end try
`
	cmd := exec.Command("osascript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("osascript: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	s := strings.TrimSpace(string(out))
	if strings.HasPrefix(s, "ERROR:") {
		return nil, "", fmt.Errorf("osascript: %s", strings.TrimPrefix(s, "ERROR:"))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, "", fmt.Errorf("decode hex: %w", err)
	}
	return raw, "image/png", nil
}

// captureClipboardLinux tries xclip first
// (X11), then wl-paste (Wayland). Both write
// raw image bytes to stdout when the format
// is image/png.
func captureClipboardLinux() ([]byte, string, error) {
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			raw := stdout.Bytes()
			if len(raw) > 0 {
				return raw, "image/png", nil
			}
		}
		// Fall through to wl-paste on xclip
		// failure — could be a Wayland session.
	}
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command("wl-paste", "--type", "image/png")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			raw := stdout.Bytes()
			if len(raw) > 0 {
				return raw, "image/png", nil
			}
		}
	}
	return nil, "", fmt.Errorf("send_screenshot: no clipboard tool available (install xclip or wl-paste)")
}
