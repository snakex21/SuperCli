package sandbox

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type downloadExportKey struct{}
type pinnedDownloadDestinationKey struct{}

type downloadExportDir struct {
	path      string
	canonical string
}

type downloadExportFile struct {
	path            string
	canonical       string
	parent          string
	canonicalParent string
}

type downloadExportTargets struct {
	dirs  []downloadExportDir
	files []downloadExportFile
}

const maxDownloadExportTargets = 64

// WithDownloadExportDirs grants this invocation permission to create downloads
// in folders explicitly requested by the human. Grants are not tool arguments
// and do not change permissions for other file tools or subsequent turns.
// Missing folder suffixes are allowed; symlinks are pinned to their current
// canonical root. Invalid, relative and system-controlled roots fail closed.
func WithDownloadExportDirs(ctx context.Context, dirs ...string) context.Context {
	return WithDownloadExportTargets(ctx, dirs, nil)
}

// WithDownloadExportTargets replaces this invocation's directory and exact-file
// grants together. A file grant never authorizes its parent, siblings or child
// paths. Both kinds pin existing symlink/junction ancestors at admission.
func WithDownloadExportTargets(ctx context.Context, dirs, files []string) context.Context {
	grants := downloadExportTargets{}
	for _, dir := range dirs {
		if len(grants.dirs)+len(grants.files) >= maxDownloadExportTargets {
			break
		}
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		canonical, err := canonicalHomePath(dir)
		if err != nil || isSensitive(canonical) {
			continue
		}
		grants.dirs = append(grants.dirs, downloadExportDir{path: dir, canonical: canonical})
	}
	for _, file := range files {
		if len(grants.dirs)+len(grants.files) >= maxDownloadExportTargets {
			break
		}
		if !filepath.IsAbs(file) {
			continue
		}
		file = filepath.Clean(file)
		parent := filepath.Dir(file)
		canonicalParent, err := canonicalHomePath(parent)
		if err != nil || isSensitive(canonicalParent) {
			continue
		}
		canonical, err := resolvePath(canonicalParent, file, true)
		if err != nil || downloadPathEqual(canonical, canonicalParent) {
			continue
		}
		grants.files = append(grants.files, downloadExportFile{path: file, canonical: canonical, parent: parent, canonicalParent: canonicalParent})
	}
	// Replace, rather than merge, so an invocation cannot inherit an earlier
	// turn's destinations merely by receiving that turn's context.
	return context.WithValue(ctx, downloadExportKey{}, grants)
}

// ResolveDownloadDestination applies the ordinary workspace boundary, the
// explicit agent allow-all setting, or this invocation's human export grant.
// An export grant only admits absolute file paths below a granted folder or
// equal to an explicitly granted file.
// Callers must still refuse existing destinations and atomically publish new
// files without overwrite. Sensitive system paths are blocked in all modes.
func ResolveDownloadDestination(ctx context.Context, workspace, path string) (string, error) {
	full, err := resolveDownloadDestination(ctx, workspace, path)
	if err != nil {
		return "", err
	}
	if pinned, ok := ctx.Value(pinnedDownloadDestinationKey{}).(string); ok && !downloadPathEqual(full, pinned) {
		return "", fmt.Errorf("download destination changed after admission: %w", ErrEscape)
	}
	return full, nil
}

// WithPinnedDownloadDestination binds a single tool call to the canonical
// destination admitted before a workspace checkpoint. Rechecks must resolve
// to that exact path even if a workspace junction changes in the meantime.
func WithPinnedDownloadDestination(ctx context.Context, workspace, path string) (context.Context, error) {
	full, err := ResolveDownloadDestination(ctx, workspace, path)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, pinnedDownloadDestinationKey{}, full), nil
}

func resolveDownloadDestination(ctx context.Context, workspace, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	full, err := ResolveWithin(workspace, path)
	if err == nil || !errors.Is(err, ErrEscape) {
		return full, err
	}
	if IsUnsandboxed() {
		return ResolveSafe(workspace, path)
	}
	if filepath.IsAbs(path) {
		grants, _ := ctx.Value(downloadExportKey{}).(downloadExportTargets)
		for _, grant := range grants.files {
			if !downloadPathEqual(path, grant.path) && !downloadPathEqual(path, grant.canonical) {
				continue
			}
			parent, rootErr := canonicalHomePath(grant.parent)
			if rootErr != nil || !downloadPathEqual(parent, grant.canonicalParent) {
				return "", fmt.Errorf("download export file parent changed: %w", ErrEscape)
			}
			resolved, resolveErr := resolvePath(grant.canonicalParent, path, true)
			if resolveErr != nil {
				return "", resolveErr
			}
			if !downloadPathEqual(resolved, grant.canonical) {
				return "", fmt.Errorf("download export file changed: %w", ErrEscape)
			}
			return resolved, nil
		}
		for _, grant := range grants.dirs {
			// Check the caller's spelling as well as the pinned canonical root;
			// this lets explicitly requested junction/short-name roots work.
			if !IsUnder(grant.path, path) && !IsUnder(grant.canonical, path) {
				continue
			}
			current, rootErr := canonicalHomePath(grant.path)
			if rootErr != nil || !downloadPathEqual(current, grant.canonical) {
				return "", fmt.Errorf("download export folder changed: %w", ErrEscape)
			}
			resolved, resolveErr := resolvePath(grant.canonical, path, true)
			if resolveErr != nil {
				return "", resolveErr
			}
			if downloadPathEqual(resolved, grant.canonical) {
				return "", fmt.Errorf("download destination must be a new file below the export folder: %w", ErrEscape)
			}
			return resolved, nil
		}
	}
	return "", fmt.Errorf("download destination is outside the project and this turn's requested export folders or exact filenames: %w", ErrEscape)
}

func downloadPathEqual(a, b string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
