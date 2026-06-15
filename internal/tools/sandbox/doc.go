// Package sandbox enforces path and env restrictions
// for file and bash tools. F7 ships a minimal policy:
//
//   * ResolveSafe refuses paths that escape the home
//     (via "..", absolute paths, or symlinks).
//   * AllowDestructive gates file_write, file_delete,
//     and bash commands against the home and the
//     sensitive system paths (/dev, /proc, /sys).
//   * ScrubEnv strips API keys, tokens, and other
//     credentials from the environment before passing
//     it to sub-commands.
//
// The package does NOT try to be a full Linux MAC layer.
// F8+ may add a TOML-driven policy override; for F7 the
// rules are hard-coded and well-tested.
package sandbox

import "errors"

// ErrEscape is returned by ResolveSafe when a path
// tries to break out of the home via "..", a symlink
// chain, or an absolute path outside the home.
var ErrEscape = errors.New("sandbox: path escapes home")

// ErrDenied is returned by AllowDestructive when a
// write/delete/bash operation targets a location the
// policy forbids (e.g. /etc, /dev, /proc, /sys).
var ErrDenied = errors.New("sandbox: operation denied by policy")
