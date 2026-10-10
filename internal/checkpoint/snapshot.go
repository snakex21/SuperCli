package checkpoint

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Limits are checked before writing any blobs. Arbitrary commands need a full
// baseline; named file tools can still edit a small file in a huge workspace.
const (
	maxSnapshotFileBytes = int64(256 << 20)
	maxSnapshotBytes     = int64(1 << 30)
	maxSnapshotFiles     = 20_000
)

var ErrSnapshotLimit = errors.New("checkpoint snapshot limit exceeded")

type snapshotFile struct {
	path string
	info os.FileInfo
}

func snapshotLimit(reason string) error {
	return fmt.Errorf("%w: %s. Use file tools with explicit paths for small edits, or reduce the command's workspace scope", ErrSnapshotLimit, reason)
}

func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pathPrefix(p, prefix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix))
	}
	return strings.HasPrefix(p, prefix)
}

func (m *Manager) snapshotExcluded(p string) bool {
	if m.applicationDataPath(p) {
		return true
	}
	for _, part := range strings.Split(p, "/") {
		if pathEqual(part, ".git") || pathEqual(part, ".supercli") || pathEqual(part, "checkpoints") {
			return true
		}
	}
	return false
}

// preflightSnapshot only reads file metadata. In particular a rejected full
// capture never begins copying a large archive or half a million source files.
func (m *Manager) preflightSnapshot(ctx context.Context, roots, protected []string) ([]snapshotFile, error) {
	files := make([]snapshotFile, 0)
	seen := map[string]bool{}
	var total int64
	add := func(p string, info os.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		p = filepath.ToSlash(p)
		if m.snapshotExcluded(p) || scopeContains(protected, p) {
			return nil
		}
		key := p
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return nil
		}
		seen[key] = true
		if info == nil {
			var err error
			info, err = os.Lstat(filepath.Join(m.home, filepath.FromSlash(p)))
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint cannot safely restore non-regular path %q", p)
		}
		if info.Size() > maxSnapshotFileBytes {
			return snapshotLimit(fmt.Sprintf("%q is %d bytes (file limit %d)", p, info.Size(), maxSnapshotFileBytes))
		}
		total += info.Size()
		if total > maxSnapshotBytes {
			return snapshotLimit(fmt.Sprintf("selected files exceed %d bytes", maxSnapshotBytes))
		}
		if len(files) >= maxSnapshotFiles {
			return snapshotLimit(fmt.Sprintf("selected files exceed %d files", maxSnapshotFiles))
		}
		files = append(files, snapshotFile{path: p, info: info})
		return nil
	}
	if roots == nil {
		if err := m.eachWorkspacePath(ctx, func(p string) error { return add(p, nil) }); err != nil {
			return nil, err
		}
	} else {
		visited := 0
		var walk func(string, os.FileInfo) error
		walk = func(p string, info os.FileInfo) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(m.home, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if m.snapshotExcluded(rel) || scopeContains(protected, rel) {
				return nil
			}
			visited++
			if visited > maxSnapshotFiles*2 {
				return snapshotLimit("selected directory contains too many entries")
			}
			if !info.IsDir() {
				return add(rel, info)
			}
			directory, err := os.Open(p)
			if err != nil {
				return err
			}
			defer directory.Close()
			for {
				// ReadDir(n) caps memory even for a huge, flat directory.
				entries, readErr := directory.ReadDir(128)
				for _, entry := range entries {
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if err := walk(filepath.Join(p, entry.Name()), info); err != nil {
						return err
					}
				}
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				if readErr != nil {
					return readErr
				}
			}
		}
		for _, root := range roots {
			full := filepath.Join(m.home, filepath.FromSlash(root))
			if !within(m.home, full) {
				return nil, fmt.Errorf("unsafe checkpoint path %q", root)
			}
			info, err := os.Lstat(full)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := walk(full, info); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

// Paths are streamed and capped instead of buffering Git's entire output.
func (m *Manager) eachWorkspacePath(ctx context.Context, visit func(string) error) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := m.gitCommand(childCtx, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stream, err := startCheckpointCommandStream(childCtx, cmd)
	if err != nil {
		return err
	}
	stdout := stream.Stdout
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), 128<<10)
	scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if end := bytes.IndexByte(data, 0); end >= 0 {
			return end + 1, data[:end], nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, errors.New("unterminated checkpoint workspace path")
		}
		return 0, nil, nil
	})
	var readErr error
	count := 0
	for scan.Scan() {
		count++
		if count > maxSnapshotFiles {
			readErr = snapshotLimit(fmt.Sprintf("workspace enumeration exceeds %d paths", maxSnapshotFiles))
			break
		}
		if readErr = visit(scan.Text()); readErr != nil {
			break
		}
	}
	if readErr == nil {
		readErr = scan.Err()
	}
	if readErr != nil {
		cancel()
	}
	waitErr := stream.Wait()
	if readErr != nil {
		return errors.Join(ctx.Err(), readErr, waitErr)
	}
	if waitErr != nil {
		return fmt.Errorf("checkpoint enumerate: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (m *Manager) captureSnapshot(ctx context.Context, roots []string, base string, protected []string) (string, error) {
	return m.captureSnapshotWithExtras(ctx, roots, base, protected, nil)
}

func (m *Manager) captureSnapshotWithExtras(ctx context.Context, roots []string, base string, protected, extras []string) (string, error) {
	return m.captureSnapshotFiltered(ctx, roots, base, protected, extras, false)
}

func (m *Manager) captureSnapshotFiltered(ctx context.Context, roots []string, base string, protected, extras []string, ignoredMissingOnly bool) (string, error) {
	return m.captureSnapshotFilteredPinned(ctx, roots, base, protected, extras, ignoredMissingOnly, nil, "")
}

func (m *Manager) captureSnapshotFilteredPinned(ctx context.Context, roots []string, base string, protected, extras []string, ignoredMissingOnly bool, pins *activePins, side string) (oid string, err error) {
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	finishUsage, err := m.beginUsageLocked(ctx)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, finishUsage(err == nil)) }()
	if err := m.ensureRepoLocked(ctx); err != nil {
		return "", err
	}
	files, err := m.preflightSnapshot(ctx, roots, protected)
	if err != nil {
		return "", err
	}
	if len(extras) > 0 {
		additional, err := m.preflightSnapshot(ctx, extras, protected)
		if err != nil {
			return "", err
		}
		seen := make(map[string]bool, len(files))
		key := func(p string) string {
			if runtime.GOOS == "windows" {
				return strings.ToLower(p)
			}
			return p
		}
		for _, file := range files {
			seen[key(file.path)] = true
		}
		for _, file := range additional {
			if !seen[key(file.path)] {
				files = append(files, file)
				seen[key(file.path)] = true
			}
		}
	}
	if ignoredMissingOnly {
		files, err = m.ignoredFilesMissingFromBase(ctx, base, files)
		if err != nil {
			return "", err
		}
	}
	if err := m.checkExpandedSnapshot(ctx, base, files); err != nil {
		return "", err
	}
	// The index is private even relative to other captures, and lives beside
	// the app's checkpoint repository. No Git index in the workspace is used.
	index, err := os.CreateTemp(filepath.Dir(m.repo), ".snapshot-index-")
	if err != nil {
		return "", err
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		return "", err
	}
	defer func() {
		for _, path := range []string{indexPath, indexPath + ".lock"} {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	if err := os.Remove(indexPath); err != nil {
		return "", err
	}
	readTree := []string{"read-tree", "--empty"}
	if base != "" {
		readTree = []string{"read-tree", base}
	}
	if _, err := m.gitIndex(ctx, indexPath, nil, readTree...); err != nil {
		return "", err
	}
	var entries bytes.Buffer
	var encoder blobEncoder
	for _, file := range files {
		hash, err := m.storeRawBlobWithEncoder(ctx, file, &encoder)
		if err != nil {
			return "", err
		}
		mode := "100644"
		if file.info.Mode()&0111 != 0 {
			mode = "100755"
		}
		fmt.Fprintf(&entries, "%s %s\t%s%c", mode, hash, file.path, 0)
	}
	if entries.Len() > 0 {
		if _, err := m.gitIndex(ctx, indexPath, &entries, "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	tree, err := m.gitIndex(ctx, indexPath, nil, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := m.commitTreeLocked(ctx, strings.TrimSpace(tree))
	if err != nil {
		return "", err
	}
	if pins != nil {
		if err := m.prepareRefUpdatesLocked(pins.root() + "/" + side); err != nil {
			return "", err
		}
		if err := pins.publish(ctx, side, commit); err != nil {
			return "", err
		}
		if err := m.accountRefsLocked(1); err != nil {
			return "", err
		}
	}
	return commit, nil
}

func (m *Manager) ignoredFilesMissingFromBase(ctx context.Context, base string, files []snapshotFile) ([]snapshotFile, error) {
	paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool)
	key := func(p string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(p)
		}
		return p
	}
	for _, p := range strings.Split(paths, "\x00") {
		existing[key(p)] = true
	}
	var input strings.Builder
	for _, file := range files {
		if !existing[key(file.path)] {
			input.WriteString(file.path)
			input.WriteByte(0)
		}
	}
	if input.Len() == 0 {
		return nil, nil
	}
	cmd := m.gitCommand(ctx, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(input.String())
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("checkpoint ignored paths: %w: %s", err, stderr.String())
		}
	}
	ignored := make(map[string]bool)
	for _, p := range strings.Split(out.String(), "\x00") {
		ignored[key(p)] = true
	}
	kept := files[:0]
	for _, file := range files {
		if !existing[key(file.path)] && ignored[key(file.path)] {
			kept = append(kept, file)
		}
	}
	return kept, nil
}

func (m *Manager) checkExpandedSnapshot(ctx context.Context, base string, files []snapshotFile) error {
	sizes := make(map[string]int64)
	var shape StoreUsageTreeShape
	key := func(p string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(p)
		}
		return p
	}
	if base != "" {
		out, err := m.git(ctx, "ls-tree", "-r", "-l", "-z", base)
		if err != nil {
			return err
		}
		for _, entry := range strings.Split(out, "\x00") {
			if entry == "" {
				continue
			}
			tab := strings.IndexByte(entry, '\t')
			if tab < 0 {
				return errors.New("invalid checkpoint tree entry")
			}
			fields := strings.Fields(entry[:tab])
			if len(fields) != 4 {
				return errors.New("invalid checkpoint tree metadata")
			}
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 {
				return errors.New("invalid checkpoint blob size")
			}
			sizes[key(entry[tab+1:])] = size
			if err := shape.AddPath(entry[tab+1:]); err != nil {
				return err
			}
		}
	}
	for _, file := range files {
		sizes[key(file.path)] = file.info.Size()
		if err := shape.AddPath(file.path); err != nil {
			return err
		}
	}
	var total int64
	for _, size := range sizes {
		total += size
	}
	if len(sizes) > maxSnapshotFiles || total > maxSnapshotBytes {
		return snapshotLimit(fmt.Sprintf("whole turn exceeds %d files or %d bytes", maxSnapshotFiles, maxSnapshotBytes))
	}
	return m.accountSnapshotTreeShapeLocked(shape)
}

func (m *Manager) gitIndex(ctx context.Context, index string, input io.Reader, args ...string) (string, error) {
	cmd := m.gitCommand(ctx, args...)
	cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
	cmd.Stdin = input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("checkpoint %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) commitTreeLocked(ctx context.Context, tree string) (string, error) {
	cmd := m.gitCommand(ctx, "commit-tree", tree, "-m", "SuperCli checkpoint")
	// Identical trees produce identical commits, including no-op turns.
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=SuperCli", "GIT_AUTHOR_EMAIL=checkpoint@local", "GIT_COMMITTER_NAME=SuperCli", "GIT_COMMITTER_EMAIL=checkpoint@local", "GIT_AUTHOR_DATE=1970-01-01T00:00:00+0000", "GIT_COMMITTER_DATE=1970-01-01T00:00:00+0000")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("checkpoint commit: %w: %s", err, out)
	}
	commit := strings.TrimSpace(string(out))
	if _, err := m.git(ctx, "update-ref", "refs/supercli/latest", commit); err != nil {
		return "", err
	}
	if err := m.accountRefsLocked(1); err != nil {
		return "", err
	}
	return commit, nil
}

// Store byte-for-byte blobs without executing project attributes, clean
// filters, LFS, encoding conversion, or line-ending normalization. Streaming
// exactly the preflight length bounds both memory and writes if a file grows.
func (m *Manager) storeRawBlob(ctx context.Context, file snapshotFile) (string, error) {
	var encoder blobEncoder
	return m.storeRawBlobWithEncoder(ctx, file, &encoder)
}

// The encoder belongs to one capture, whose blobs are written sequentially.
// Allocate compression state only for new objects and release it when that
// capture ends, rather than retaining a compressor on the manager or in a pool.
type blobEncoder struct {
	compressed *zlib.Writer
	buffer     []byte
}

func (e *blobEncoder) writer(output io.Writer) *zlib.Writer {
	if e.compressed == nil {
		e.compressed = zlib.NewWriter(output)
	} else {
		e.compressed.Reset(output)
	}
	return e.compressed
}

func (e *blobEncoder) copyN(dst io.Writer, src io.Reader, size int64) error {
	if e.buffer == nil {
		e.buffer = make([]byte, 32<<10)
	}
	n, err := io.CopyBuffer(dst, io.LimitReader(src, size), e.buffer)
	if n == size {
		return nil
	}
	if err == nil {
		err = io.EOF
	}
	return err
}

func (m *Manager) storeRawBlobWithEncoder(ctx context.Context, file snapshotFile, encoder *blobEncoder) (string, error) {
	input, err := os.Open(filepath.Join(m.home, filepath.FromSlash(file.path)))
	if err != nil {
		return "", err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(file.info, info) || !info.Mode().IsRegular() || info.Size() != file.info.Size() || !info.ModTime().Equal(file.info.ModTime()) {
		return "", fmt.Errorf("checkpoint source changed while capturing %q", file.path)
	}
	objects := filepath.Join(m.repo, "objects")
	hash := sha1.New()
	if _, err := fmt.Fprintf(hash, "blob %d%c", info.Size(), 0); err != nil {
		return "", err
	}
	if err := encoder.copyN(hash, &contextReader{ctx: ctx, reader: input}, info.Size()); err != nil {
		return "", fmt.Errorf("checkpoint read %q: %w", file.path, err)
	}
	if err := unchangedSnapshotInput(input, info); err != nil {
		return "", fmt.Errorf("checkpoint source changed while capturing %q: %w", file.path, err)
	}
	key := hex.EncodeToString(hash.Sum(nil))
	object := filepath.Join(objects, key[:2], key[2:])
	if _, err := os.Stat(object); err == nil {
		return key, nil // Deduplicate unchanged content without recompressing it.
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	output, err := os.CreateTemp(objects, "checkpoint-blob-")
	if err != nil {
		return "", err
	}
	temp := output.Name()
	defer os.Remove(temp)
	defer output.Close()
	counted := checkpointCountingWriter{writer: output}
	compressed := encoder.writer(&counted)
	defer compressed.Close()
	hash.Reset()
	writer := io.MultiWriter(hash, compressed)
	if _, err := fmt.Fprintf(writer, "blob %d%c", info.Size(), 0); err != nil {
		return "", err
	}
	if err := encoder.copyN(writer, &contextReader{ctx: ctx, reader: input}, info.Size()); err != nil {
		return "", fmt.Errorf("checkpoint read %q: %w", file.path, err)
	}
	if err := unchangedSnapshotInput(input, info); err != nil || hex.EncodeToString(hash.Sum(nil)) != key {
		return "", fmt.Errorf("checkpoint source changed while capturing %q", file.path)
	}
	if err := compressed.Close(); err != nil {
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(object), 0700); err != nil {
		return "", err
	}
	if err := os.Rename(temp, object); err != nil {
		return "", err
	}
	if err := m.accountObjectLocked(counted.bytes); err != nil {
		return "", err
	}
	return key, nil
}

func unchangedSnapshotInput(input *os.File, info os.FileInfo) error {
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.New("file length changed")
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) {
		return errors.New("file metadata changed")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func checkpointGitEnv() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(item), "GIT_") {
			result = append(result, item)
		}
	}
	return result
}

func (m *Manager) gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	// Per-command long-path support keeps portable stores usable on Windows
	// without changing the user's Git configuration or system settings.
	base := []string{"--git-dir=" + m.repo, "--work-tree=" + m.home, "-c", "core.longpaths=true", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "core.logAllRefUpdates=false", "-c", "core.splitIndex=false", "-c", "i18n.commitEncoding=UTF-8"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = m.home
	cmd.Env = checkpointGitEnv()
	configureCheckpointCommand(cmd)
	return cmd
}
