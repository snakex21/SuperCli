package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceCleanupPreview describes private checkpoint files only. Clearing
// them removes Undo/Redo for all conversations sharing this workspace, while
// leaving conversation rows, project memory and workspace files untouched.
type WorkspaceCleanupPreview struct {
	Workspace string `json:"workspace"`
	Bytes     int64  `json:"bytes"`
	Files     int    `json:"files"`
	Stores    int    `json:"stores"`
}

type workspaceCleanupFile struct {
	path  string
	bytes int64
	proof *retentionFileProof
}

type workspaceCleanupDirectory struct {
	path string
	info os.FileInfo
}

type workspaceCleanupStore struct {
	root  string
	files []workspaceCleanupFile
	dirs  []workspaceCleanupDirectory
	bytes int64
}

// PreviewWorkspaceCleanup performs one on-demand, bounded metadata walk of
// this workspace's exact hash directories. It reads no blobs and does not
// initialize missing stores, recover transactions, or inspect other projects.
func PreviewWorkspaceCleanup(ctx context.Context, home, dataDir string) (out WorkspaceCleanupPreview, err error) {
	return workspaceCleanup(ctx, home, dataDir, false)
}

// ClearWorkspaceCheckpoints clears the same explicitly selected stores as the
// preview. The single portable gate covers fresh lease/ref guards and deletion.
// On an interrupted deletion the receipt reports only confirmed removed files;
// the usage ledger remains dirty and retrying is safe. Unknown archives fail
// closed: a matching badcheckpoints hash also needs valid bounded turns.json.
func ClearWorkspaceCheckpoints(ctx context.Context, home, dataDir string) (out WorkspaceCleanupPreview, err error) {
	return workspaceCleanup(ctx, home, dataDir, true)
}

func workspaceCleanup(ctx context.Context, home, dataDir string, remove bool) (out WorkspaceCleanupPreview, err error) {
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if strings.TrimSpace(home) == "" {
		return out, errors.New("checkpoint cleanup requires an explicit workspace")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return out, err
	}
	// Match Open's lexical absolute path key. A link alias is not authority to
	// remove another registered workspace's history at its resolved target.
	out.Workspace = filepath.Clean(abs)
	gate, err := NewStoreGate(dataDir)
	if err != nil {
		return out, err
	}
	transaction, err := gate.Acquire(ctx)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, transaction.Close()) }()
	data := filepath.Dir(gate.path)
	// A prepared collector transaction may still publish old record metadata.
	// Neither preview nor explicit cleanup may replay or race that transaction.
	journal := filepath.Join(data, filepath.FromSlash(retentionJournalName))
	if err := retentionSafePath(data, journal); err != nil {
		return out, err
	}
	if _, err := os.Lstat(journal); err == nil {
		return out, fmt.Errorf("%w: checkpoint retention recovery is pending", ErrStoreBusy)
	} else if !os.IsNotExist(err) {
		return out, err
	}
	key := workspaceCheckpointKey(out.Workspace)
	var plans []workspaceCleanupStore
	entries := 0
	for _, kind := range []string{"checkpoints", "badcheckpoints"} {
		root := filepath.Join(data, kind, key)
		if err := retentionSafePath(data, root); err != nil {
			return out, err
		}
		info, statErr := os.Lstat(root)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return out, statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return out, ErrStoreInventory
		}
		plan := workspaceCleanupStore{root: root}
		if err := walkWorkspaceCleanup(ctx, data, root, 0, &entries, &plan, remove); err != nil {
			return out, err
		}
		if err := validateCleanupMetadata(root, kind == "badcheckpoints"); err != nil {
			return out, err
		}
		for _, rel := range []string{"commondir", "shallow", "objects/info/alternates", "objects/info/http-alternates", "info/grafts"} {
			if _, err := os.Lstat(filepath.Join(root, "objects.git", filepath.FromSlash(rel))); err == nil || !os.IsNotExist(err) {
				return out, fmt.Errorf("%w: unsupported checkpoint repository layout", ErrStoreInventory)
			}
		}
		// Ref inspection must not depend on the project folder still existing.
		// No workspace command is run; the cwd is the portable data directory.
		guard := &Manager{home: data, repo: filepath.Join(root, "objects.git"), meta: filepath.Join(root, "turns.json"), gate: gate}
		if err := guard.requireNoActiveTurnLocked(ctx); err != nil {
			return out, err
		}
		if err := probeCleanupArchiveLock(root); err != nil {
			return out, err
		}
		plans = append(plans, plan)
	}
	if !remove {
		for _, plan := range plans {
			out.Bytes += plan.bytes
			out.Files += len(plan.files)
			out.Stores++
		}
		return out, ctx.Err()
	}
	if len(plans) == 0 {
		return out, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	counter, err := NewStoreUsageCounter(gate)
	if err != nil {
		return out, err
	}
	if _, err := counter.BeginLocked(ctx); err != nil {
		return out, err
	}
	for _, plan := range plans {
		if err := removeWorkspaceCleanupStore(ctx, data, plan, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

func workspaceCheckpointKey(home string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(home))))
	return hex.EncodeToString(hash[:8])
}

func validateCleanupMetadata(root string, archive bool) error {
	metadata, err := readCheckpointMetadata(filepath.Join(root, "turns.json"))
	if os.IsNotExist(err) && !archive {
		return nil // An accepted owner may precede its first capture.
	}
	if err != nil {
		return fmt.Errorf("%w: unrecognized checkpoint metadata at %s: %v", ErrStoreInventory, root, err)
	}
	var records []Record
	if json.Unmarshal(metadata, &records) != nil || records == nil {
		return fmt.Errorf("%w: unrecognized checkpoint metadata at %s", ErrStoreInventory, root)
	}
	for _, record := range records {
		if record.ID == "" || record.SessionID == "" ||
			(record.Before != "" && !validRetentionOID(record.Before, 40)) ||
			(record.After != "" && !validRetentionOID(record.After, 40)) {
			return fmt.Errorf("%w: unrecognized checkpoint record at %s", ErrStoreInventory, root)
		}
	}
	return nil
}

func probeCleanupArchiveLock(root string) error {
	path := filepath.Join(root, ".narrowing.lock")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	unlock, err := checkpointStoreLock(path)
	if err != nil {
		return err
	}
	return unlock()
}

func walkWorkspaceCleanup(ctx context.Context, data, path string, depth int, entries *int, plan *workspaceCleanupStore, proofs bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > retentionMaxDepth {
		return ErrStoreInventory
	}
	*entries = *entries + 1
	if *entries > retentionMaxEntries {
		return ErrStoreInventory
	}
	if err := retentionSafePath(data, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrStoreInventory
	}
	if info.Mode().IsRegular() {
		file := workspaceCleanupFile{path: path, bytes: info.Size()}
		if proofs {
			file.proof, err = retentionProveFile(path, info)
			if err != nil {
				return err
			}
		}
		plan.bytes, err = retentionAdd(plan.bytes, info.Size())
		if err != nil {
			return err
		}
		plan.files = append(plan.files, file)
		return nil
	}
	if !info.IsDir() {
		return ErrStoreInventory
	}
	plan.dirs = append(plan.dirs, workspaceCleanupDirectory{path: path, info: info})
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		children, readErr := directory.ReadDir(128)
		for _, child := range children {
			if err := walkWorkspaceCleanup(ctx, data, filepath.Join(path, child.Name()), depth+1, entries, plan, proofs); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func removeWorkspaceCleanupStore(ctx context.Context, data string, plan workspaceCleanupStore, out *WorkspaceCleanupPreview) error {
	for _, file := range plan.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := retentionSafePath(data, file.path); err != nil {
			return err
		}
		if err := retentionRemoveProven(file.path, file.proof); err != nil {
			return err
		}
		out.Bytes += file.bytes
		out.Files++
	}
	for i := len(plan.dirs) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := plan.dirs[i]
		if err := retentionSafePath(data, dir.path); err != nil {
			return err
		}
		info, err := os.Lstat(dir.path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(dir.info, info) {
			return ErrStoreInventory
		}
		if err := os.Remove(dir.path); err != nil {
			return err
		}
	}
	out.Stores++
	return nil
}
