// office_write.go holds the shared write-path helpers for the
// "wave 2" office editing tools (edit_docx, edit_xlsx,
// file_ops). The rules every office write tool follows:
//
//  1. Paths are validated through the sandbox: relative paths
//     resolve against the tool's BaseDir (the user's home/work
//     dir) and may not escape it or land on a sensitive system
//     root (sandbox.ResolveSafe).
//  2. Writes are never in-place. We build the new file in a
//     temp file in the SAME directory as the target (so the
//     final rename is not cross-volume), and only swap it in
//     when fully written.
//  3. Before replacing an existing file, a backup copy is
//     created next to it as "<name>.bak" (overwriting an older
//     .bak). Office users need an undo.
package core

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/sandbox"
)

// resolveSandboxed resolves a user-supplied path against
// baseDir through the sandbox. The returned path is absolute,
// symlink-resolved, inside baseDir, and not a sensitive
// system root.
func resolveSandboxed(baseDir, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	resolved, err := sandbox.ResolveSafe(baseDir, p)
	if err != nil {
		return "", fmt.Errorf("path %q is not allowed: %w (paths must stay inside %s)", p, err, baseDir)
	}
	return resolved, nil
}

// ResolveSandboxed is the exported form of resolveSandboxed for tool
// implementation subpackages. The top-level tools package keeps the public
// facade; domain packages call this shared safety helper directly.
func ResolveSandboxed(baseDir, p string) (string, error) {
	return resolveSandboxed(baseDir, p)
}

// copyFile copies src to dst (creating/truncating dst),
// preserving the source's permission bits.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// backupAndReplace swaps tmpPath into place at target.
// If target already exists, a copy is written to
// "<target>.bak" first (replacing any previous .bak).
// Returns (backupPath, error); backupPath is "" when the
// target did not exist before.
func backupAndReplace(target, tmpPath string) (string, error) {
	backup := ""
	if _, err := os.Lstat(target); err == nil {
		backup = target + ".bak"
		if err := copyFile(target, backup); err != nil {
			return "", fmt.Errorf("create backup %q: %w", backup, err)
		}
		// Windows os.Rename refuses to replace an
		// existing file, so remove the original first.
		// The .bak copy makes this safe: if the rename
		// below fails, the content survives in the .bak.
		if err := os.Remove(target); err != nil {
			return backup, fmt.Errorf("remove old %q: %w", target, err)
		}
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return backup, fmt.Errorf("move new file into place: %w", err)
	}
	return backup, nil
}

// BackupAndReplace exposes the safe replace protocol to document/tool
// implementation subpackages.
func BackupAndReplace(target, tmpPath string) (string, error) {
	return backupAndReplace(target, tmpPath)
}

// rewriteZipEntry writes a new zip at dstPath that is a
// byte-for-byte copy of the zip at srcPath, EXCEPT that the
// entry named entryName carries newData. Untouched entries are
// copied raw (compressed bytes verbatim), so everything else
// in the archive — styles, images, metadata, macros — is
// preserved exactly. If the entry does not exist in the
// source it is appended.
func rewriteZipEntry(srcPath, dstPath, entryName string, newData []byte) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	out, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create temp zip: %w", err)
	}
	zw := zip.NewWriter(out)
	wrote := false
	for _, f := range zr.File {
		if f.Name == entryName {
			hdr := f.FileHeader // copy
			hdr.CompressedSize = 0
			hdr.CompressedSize64 = 0
			hdr.UncompressedSize = 0
			hdr.UncompressedSize64 = 0
			hdr.CRC32 = 0
			w, err := zw.CreateHeader(&hdr)
			if err != nil {
				out.Close()
				return fmt.Errorf("write entry %q: %w", entryName, err)
			}
			if _, err := w.Write(newData); err != nil {
				out.Close()
				return fmt.Errorf("write entry %q: %w", entryName, err)
			}
			wrote = true
			continue
		}
		// Raw copy: compressed bytes pass through
		// untouched, preserving the entry exactly.
		rc, err := f.OpenRaw()
		if err != nil {
			out.Close()
			return fmt.Errorf("open raw %q: %w", f.Name, err)
		}
		hdr := f.FileHeader
		w, err := zw.CreateRaw(&hdr)
		if err != nil {
			out.Close()
			return fmt.Errorf("copy raw %q: %w", f.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			out.Close()
			return fmt.Errorf("copy raw %q: %w", f.Name, err)
		}
	}
	if !wrote {
		w, err := zw.Create(entryName)
		if err != nil {
			out.Close()
			return fmt.Errorf("append entry %q: %w", entryName, err)
		}
		if _, err := w.Write(newData); err != nil {
			out.Close()
			return fmt.Errorf("append entry %q: %w", entryName, err)
		}
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return fmt.Errorf("finalize zip: %w", err)
	}
	return out.Close()
}

// editZipEntryInPlace runs the full safe-write protocol for a
// zip-based document: rewrite one entry into a temp file next
// to target, back up the original, atomically swap. Returns
// the backup path ("" if target was new).
func editZipEntryInPlace(target, entryName string, newData []byte) (string, error) {
	tmp := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".tmp")
	if err := rewriteZipEntry(target, tmp, entryName, newData); err != nil {
		os.Remove(tmp)
		return "", err
	}
	backup, err := backupAndReplace(target, tmp)
	if err != nil {
		os.Remove(tmp)
		return backup, err
	}
	return backup, nil
}

// EditZipEntryInPlace exposes the safe zip-entry rewrite protocol to office
// tool implementation subpackages.
func EditZipEntryInPlace(target, entryName string, newData []byte) (string, error) {
	return editZipEntryInPlace(target, entryName, newData)
}

// xmlEscapeText escapes the five XML special characters for
// use inside element text or attribute values.
func xmlEscapeText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// XMLEscapeText exposes xmlEscapeText to office tool implementation
// subpackages.
func XMLEscapeText(s string) string { return xmlEscapeText(s) }
