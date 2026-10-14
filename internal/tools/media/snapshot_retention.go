package media

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// snapshotRetentionAge is the minimum age before a snapshot may be removed.
	snapshotRetentionAge = 7 * 24 * time.Hour
	// snapshotRetentionKeep newest snapshots are always kept regardless of age.
	snapshotRetentionKeep = 20
	// snapshotSessionMediaDir mirrors session.sessionMediaDirName. The tools
	// layer must not import storage; the folder lives beside .supercli in the
	// same portable data directory.
	snapshotSessionMediaDir = "session-media"
)

// Only names produced by saveSnapshot (os.CreateTemp(source+"-*"+ext)) are
// ever considered. Hidden helper temporaries (.capture-*) and any other file a
// user may have put into the folder never match.
var snapshotFilePattern = regexp.MustCompile(`^(clipboard|screen|desktop|window|qmp|browser)-[0-9]+\.(png|jpg|gif|webp|bmp|tiff|bin)$`)

var sessionMediaFilePattern = regexp.MustCompile(`^[a-f0-9]{64}\.[a-z]+$`)

type snapshotFile struct {
	path    string
	size    int64
	modTime time.Time
}

// pruneSnapshots is best-effort retention for <data>/.supercli/snapshots.
//
// Persisted tool results keep "path"/"preview_path":"snapshot:<name>" and the
// GUI history re-reads those files by name, so a snapshot is only deleted when
// its exact bytes were already copied into session-media (content-addressed by
// SHA-256, done when the image was attached for model analysis). Additionally
// it must be older than snapshotRetentionAge and not among the
// snapshotRetentionKeep newest snapshots. All errors are ignored: retention
// never fails a capture.
func pruneSnapshots(dir, sessionMediaRoot string, now time.Time) (removed int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var files []snapshotFile
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !snapshotFilePattern.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, snapshotFile{path: path, size: info.Size(), modTime: info.ModTime()})
	}
	if len(files) <= snapshotRetentionKeep {
		return 0
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return files[i].path > files[j].path
	})
	cutoff := now.Add(-snapshotRetentionAge)
	var candidates []snapshotFile
	for _, file := range files[snapshotRetentionKeep:] {
		if file.modTime.Before(cutoff) {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) == 0 {
		return 0
	}
	copied := sessionMediaDigests(sessionMediaRoot)
	if len(copied) == 0 {
		return 0
	}
	sizes := make(map[int64]bool, len(copied))
	for _, size := range copied {
		sizes[size] = true
	}
	for _, file := range candidates {
		// Size prefilter: only hash files that could match a session copy.
		if !sizes[file.size] {
			continue
		}
		digest, ok := fileSHA256(file.path)
		if !ok {
			continue
		}
		if size, found := copied[digest]; !found || size != file.size {
			continue
		}
		if os.Remove(file.path) == nil {
			removed++
		}
	}
	return removed
}

// sessionMediaDigests maps the SHA-256 names of session-media/<sid-hash>/<sha>.<ext>
// files to their sizes. Hidden quarantine/temporary entries are skipped.
func sessionMediaDigests(root string) map[string]int64 {
	out := map[string]int64{}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, dir := range dirs {
		if !dir.IsDir() || strings.HasPrefix(dir.Name(), ".") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, dir.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			name := file.Name()
			if !file.Type().IsRegular() || !sessionMediaFilePattern.MatchString(name) {
				continue
			}
			info, err := file.Info()
			if err != nil {
				continue
			}
			out[name[:64]] = info.Size()
		}
	}
	return out
}

func fileSHA256(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
