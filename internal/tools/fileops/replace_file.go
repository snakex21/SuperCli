package fileops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ReplaceFileContents writes data to path without ever writing THROUGH a hard
// link: an existing file is replaced by a sibling temp file + rename, which
// breaks a hard link (e.g. a pnpm node_modules file linked into the global
// store outside the workspace) and leaves the other link untouched. The
// existing permission bits are kept; a new file gets newMode.
//
// Symlinks are followed (the replacement lands on the link's target, and the
// link itself stays): a symlink inside the workspace is a deliberate grant by
// whoever created it. A missing path (including a dangling symlink) is
// created with a plain write, which creates the link target.
//
// If the rename is refused (on Windows another process may hold the file
// open without FILE_SHARE_DELETE) and the file is known to have a single
// link, it falls back to the in-place write, which cannot reach any other
// path.
func ReplaceFileContents(path string, data []byte, newMode fs.FileMode) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(path, data, newMode)
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		// Let the ordinary write produce the usual "is a directory" error.
		return os.WriteFile(path, data, newMode)
	}
	target := path
	if li, lErr := os.Lstat(path); lErr == nil && li.Mode()&fs.ModeSymlink != 0 {
		if resolved, rErr := filepath.EvalSymlinks(path); rErr == nil {
			target = resolved
		}
	}
	mode := info.Mode().Perm()
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".write-*")
	if err != nil {
		return replaceInPlaceIfSingleLink(target, data, mode, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr != nil {
		_ = os.Remove(name)
		return werr
	}
	if rerr := os.Rename(name, target); rerr != nil {
		_ = os.Remove(name)
		return replaceInPlaceIfSingleLink(target, data, mode, rerr)
	}
	return nil
}

// replaceInPlaceIfSingleLink is the fallback when the temp file + rename
// route is unavailable. It writes in place only when the file provably has
// exactly one link; otherwise the original error is returned.
func replaceInPlaceIfSingleLink(path string, data []byte, mode fs.FileMode, cause error) error {
	if n, ok := fileLinkCount(path); !ok || n != 1 {
		return cause
	}
	return os.WriteFile(path, data, mode)
}
