package checkpoint

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Bound path metadata and intermediate directories as well as file counts.
// Blob contents are never read or copied when narrowing existing snapshots.
const (
	maxNarrowPathBytes  = 32 << 20
	maxNarrowEntryBytes = 128 << 10
	maxNarrowTreeNodes  = 2 * maxSnapshotFiles
)

type narrowTreeEntry struct {
	name, mode string
	oid        [sha1.Size]byte
	tree       *narrowTreeNode
}

type narrowTreeNode struct {
	entries map[string]*narrowTreeEntry
	oid     [sha1.Size]byte
}

type narrowTree struct {
	root  *narrowTreeNode
	nodes []*narrowTreeNode // Parents are created before their children.
}

func newNarrowTree() *narrowTree {
	root := &narrowTreeNode{entries: make(map[string]*narrowTreeEntry)}
	return &narrowTree{root: root, nodes: []*narrowTreeNode{root}}
}

// narrowChangedSnapshots returns parentless commits containing only files
// selected by the record. Existing blob OIDs and modes are preserved exactly;
// absence on either side remains absence. It changes no refs or metadata.
// Callers admitting/pinning a record in one transaction use the Locked helper.
func (m *Manager) narrowChangedSnapshots(ctx context.Context, before, after string, files []string) (minimalBefore, minimalAfter string, err error) {
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	finishUsage, err := m.beginUsageLocked(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, finishUsage(err == nil)) }()
	return m.narrowChangedSnapshotsLocked(ctx, before, after, files)
}

// Requires the caller to hold the same store transaction used for pin/admission.
// No ensureRepo call is made: these input snapshots already belong to the store.
func (m *Manager) narrowChangedSnapshotsLocked(ctx context.Context, before, after string, files []string) (string, string, error) {
	if !validNarrowOID(before) || !validNarrowOID(after) {
		return "", "", errors.New("invalid checkpoint snapshot OID")
	}
	selected, err := narrowSelectedPaths(files)
	if err != nil {
		return "", "", err
	}
	// Validate both input trees before writing any narrowed objects.
	beforeTree, err := m.readNarrowTree(ctx, before, selected)
	if err != nil {
		return "", "", err
	}
	afterTree, err := m.readNarrowTree(ctx, after, selected)
	if err != nil {
		return "", "", err
	}
	var encoder blobEncoder // Released when this one operation ends.
	minimalBefore, err := m.writeNarrowSnapshot(ctx, beforeTree, &encoder)
	if err != nil {
		return "", "", err
	}
	minimalAfter, err := m.writeNarrowSnapshot(ctx, afterTree, &encoder)
	if err != nil {
		return "", "", err
	}
	return minimalBefore, minimalAfter, nil
}

func validNarrowOID(oid string) bool {
	if len(oid) != sha1.Size*2 || oid == strings.Repeat("0", sha1.Size*2) {
		return false
	}
	_, err := hex.DecodeString(oid)
	return err == nil && oid == strings.ToLower(oid)
}

func validNarrowPath(path string) bool {
	if !utf8.ValidString(path) || path == "" || len(path) > maxNarrowEntryBytes || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\\") {
		return false
	}
	for remaining := path; ; {
		name, tail, more := strings.Cut(remaining, "/")
		if name == "" || name == "." || name == ".." || strings.EqualFold(name, ".git") {
			return false
		}
		if !more {
			return true
		}
		remaining = tail
	}
}

func narrowSelectedPaths(files []string) (map[string]bool, error) {
	if len(files) > maxSnapshotFiles {
		return nil, snapshotLimit("changed path selection contains too many files")
	}
	selected := make(map[string]bool, len(files))
	total := 0
	for _, path := range files {
		if !validNarrowPath(path) {
			return nil, fmt.Errorf("invalid checkpoint selected path %q", path)
		}
		if selected[path] {
			continue
		}
		total += len(path)
		if total > maxNarrowPathBytes {
			return nil, snapshotLimit("changed path selection contains too much path metadata")
		}
		selected[path] = true
	}
	return selected, nil
}

func splitNarrowTreeEntry(data []byte, atEOF bool) (int, []byte, error) {
	if end := bytes.IndexByte(data, 0); end >= 0 {
		return end + 1, data[:end], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, errors.New("unterminated checkpoint tree entry")
	}
	return 0, nil, nil
}

func (m *Manager) readNarrowTree(ctx context.Context, snapshot string, selected map[string]bool) (*narrowTree, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := m.gitCommand(childCtx, "ls-tree", "--full-tree", "-r", "-z", snapshot)
	// ls-tree needs only the object database, even if the workspace is gone.
	cmd.Dir = m.repo
	stderr := &boundedRestoreError{}
	cmd.Stderr = stderr
	stream, err := startCheckpointCommandStream(childCtx, cmd)
	if err != nil {
		return nil, err
	}
	stdout := stream.Stdout
	tree := newNarrowTree()
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), maxNarrowEntryBytes+128)
	scan.Split(splitNarrowTreeEntry)
	count, total := 0, 0
	var readErr error
	for scan.Scan() {
		if readErr = ctx.Err(); readErr != nil {
			break
		}
		count++
		total += len(scan.Bytes())
		if count > maxSnapshotFiles || total > maxNarrowPathBytes {
			readErr = snapshotLimit("checkpoint tree enumeration exceeds metadata limits")
			break
		}
		entry := scan.Text()
		metadata, path, found := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !found || len(fields) != 3 || !validNarrowOID(fields[2]) || !validNarrowPath(path) {
			readErr = errors.New("invalid checkpoint tree entry")
			break
		}
		if !selected[path] {
			continue
		}
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			readErr = fmt.Errorf("checkpoint cannot retain non-regular selected path %q", path)
			break
		}
		if readErr = tree.add(path, fields[0], fields[2]); readErr != nil {
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
		return nil, errors.Join(ctx.Err(), readErr, waitErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("checkpoint narrow tree: %w: %s", waitErr, stderr.String())
	}
	return tree, nil
}

func (t *narrowTree) add(path, mode, oid string) error {
	node := t.root
	for remaining := path; ; {
		name, tail, more := strings.Cut(remaining, "/")
		existing := node.entries[name]
		if !more {
			if existing != nil {
				return fmt.Errorf("duplicate or conflicting checkpoint path %q", path)
			}
			entry := &narrowTreeEntry{name: name, mode: mode}
			if _, err := hex.Decode(entry.oid[:], []byte(oid)); err != nil {
				return err
			}
			node.entries[name] = entry
			return nil
		}
		if existing == nil {
			if len(t.nodes) >= maxNarrowTreeNodes {
				return snapshotLimit("selected checkpoint paths contain too many directories")
			}
			child := &narrowTreeNode{entries: make(map[string]*narrowTreeEntry)}
			existing = &narrowTreeEntry{name: name, mode: "40000", tree: child}
			node.entries[name] = existing
			t.nodes = append(t.nodes, child)
		} else if existing.tree == nil {
			return fmt.Errorf("conflicting checkpoint path %q", path)
		}
		node, remaining = existing.tree, tail
	}
}

func (m *Manager) writeNarrowSnapshot(ctx context.Context, tree *narrowTree, encoder *blobEncoder) (string, error) {
	var data bytes.Buffer
	for i := len(tree.nodes) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		node := tree.nodes[i]
		entries := make([]*narrowTreeEntry, 0, len(node.entries))
		for _, entry := range node.entries {
			entries = append(entries, entry)
		}
		sort.Slice(entries, func(i, j int) bool {
			// Git compares a directory name as though it ends with '/'.
			a, b := entries[i].name, entries[j].name
			if entries[i].tree != nil {
				a += "/"
			}
			if entries[j].tree != nil {
				b += "/"
			}
			return a < b
		})
		data.Reset()
		for _, entry := range entries {
			fmt.Fprintf(&data, "%s %s%c", entry.mode, entry.name, 0)
			oid := entry.oid
			if entry.tree != nil {
				oid = entry.tree.oid
			}
			data.Write(oid[:])
		}
		oid, err := m.storeNarrowGitObject(ctx, "tree", data.Bytes(), encoder)
		if err != nil {
			return "", err
		}
		if _, err := hex.Decode(node.oid[:], []byte(oid)); err != nil {
			return "", err
		}
	}
	// Match the existing deterministic snapshot identity, without update-ref
	// or a parent that could retain the original full snapshot tree.
	commit := fmt.Sprintf("tree %x\nauthor SuperCli <checkpoint@local> 0 +0000\ncommitter SuperCli <checkpoint@local> 0 +0000\n\nSuperCli checkpoint\n", tree.root.oid)
	return m.storeNarrowGitObject(ctx, "commit", []byte(commit), encoder)
}

func (m *Manager) storeNarrowGitObject(ctx context.Context, kind string, data []byte, encoder *blobEncoder) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	header := fmt.Sprintf("%s %d%c", kind, len(data), 0)
	hash := sha1.New()
	_, _ = io.WriteString(hash, header)
	_, _ = hash.Write(data)
	key := hex.EncodeToString(hash.Sum(nil))
	objects := filepath.Join(m.repo, "objects")
	object := filepath.Join(objects, key[:2], key[2:])
	if _, err := os.Stat(object); err == nil {
		return key, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	output, err := os.CreateTemp(objects, "checkpoint-narrow-")
	if err != nil {
		return "", err
	}
	temporary := output.Name()
	defer os.Remove(temporary)
	defer output.Close()
	counted := checkpointCountingWriter{writer: output}
	compressed := encoder.writer(&counted)
	defer compressed.Close()
	if _, err := io.WriteString(compressed, header); err != nil {
		return "", err
	}
	if err := encoder.copyN(compressed, &contextReader{ctx: ctx, reader: bytes.NewReader(data)}, int64(len(data))); err != nil {
		return "", err
	}
	if err := compressed.Close(); err != nil {
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(object), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, object); err != nil {
		return "", err
	}
	if err := m.accountObjectLocked(counted.bytes); err != nil {
		return "", err
	}
	return key, nil
}
