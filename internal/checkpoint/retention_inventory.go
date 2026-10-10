package checkpoint

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	retentionMaxEntries       = 200_000
	retentionMaxDepth         = 64
	retentionMaxTokens        = 2_000_000
	retentionMaxMetadata      = 16 << 20
	retentionMaxGitState      = 64 << 20
	retentionMaxReadBytes     = 256 << 20
	retentionMaxCommands      = 4096
	retentionBudgetLedgerName = ".checkpoint-budget.json"
)

type RetentionProtection struct {
	Store  string
	Reason string
}

// Counts report why physical roots remain protected without exposing workspace
// filenames, prompts or session IDs. Bytes is used only for mandatory_graph.
type RetentionRoot struct {
	Store string
	Kind  string
	Count int
	Bytes int64
}

// StoreRetentionAudit contains measured physical sizes. Unknown repositories
// are counted and protected, never omitted or treated as reclaimable estimates.
// Inventory is usable only while the caller holds the same portable StoreGate.
type StoreRetentionAudit struct {
	Inventory     RetentionInventory
	Protected     []RetentionProtection
	Roots         []RetentionRoot
	files         map[string]os.FileInfo
	proofs        map[string]*retentionFileProof
	repos         map[string]*retentionRepo
	dataDir       string
	tokens        int
	readBytes     int64
	commands      int
	measuredBytes int64
}

type retentionRepo struct {
	store      string
	repo       string
	meta       string
	raw        []json.RawMessage
	records    []Record
	metaHash   string
	expiryRaw  []byte
	expiryHash string
	refs       map[string]string
	owned      map[RetentionKey]map[string]string
	state      string
	rootCounts map[string]int
}

// AuditStoreRetentionLocked performs the expensive census on first activation,
// invalidation/crash or pressure only. It reads metadata/roots and compressed
// file sizes, never blob contents or the user's workspace/index. The bounded
// walker rejects links and special files rather than escaping the portable
// store. Graph failures conservatively protect a whole repository.
func AuditStoreRetentionLocked(ctx context.Context, dataDir string) (*StoreRetentionAudit, error) {
	a, err := retentionMeasureFilesLocked(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	return a.classifyStoreRetentionLocked(ctx)
}

type StoreRetentionSize struct {
	Bytes int64
	Files int
}

// This bounded physical census launches no Git and reads no metadata/blob
// contents. A total at or below the limit is enough to skip reachability work.
// It is a census, not a per-message fast path; a tiny shared upper-bound counter
// can avoid even this directory walk while known growth remains under budget.
func MeasureStoreRetentionLocked(ctx context.Context, dataDir string) (StoreRetentionSize, error) {
	a, err := retentionMeasureFilesLocked(ctx, dataDir)
	if err != nil {
		return StoreRetentionSize{}, err
	}
	return StoreRetentionSize{Bytes: a.measuredBytes, Files: len(a.files)}, nil
}

func retentionMeasureFilesLocked(ctx context.Context, dataDir string) (*StoreRetentionAudit, error) {
	if !filepath.IsAbs(dataDir) {
		return nil, ErrStoreInventory
	}
	a := &StoreRetentionAudit{dataDir: filepath.Clean(dataDir), files: map[string]os.FileInfo{}, proofs: map[string]*retentionFileProof{}, repos: map[string]*retentionRepo{}}
	entries := 0
	var walk func(string, int) error
	walk = func(full string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > retentionMaxEntries {
			return fmt.Errorf("%w: census exceeds %d filesystem entries", ErrStoreInventory, retentionMaxEntries)
		}
		if depth > retentionMaxDepth {
			return fmt.Errorf("%w: census exceeds %d directory levels", ErrStoreInventory, retentionMaxDepth)
		}
		info, err := os.Lstat(full)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			rel, _ := filepath.Rel(a.dataDir, full)
			a.measuredBytes, err = retentionAdd(a.measuredBytes, info.Size())
			if err != nil {
				return err
			}
			a.files[filepath.ToSlash(rel)] = info
			return nil
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrStoreInventory
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			children, readErr := f.ReadDir(128)
			for _, child := range children {
				if err := walk(filepath.Join(full, child.Name()), depth+1); err != nil {
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
	for _, root := range []string{"checkpoints", "badcheckpoints"} {
		full := filepath.Join(a.dataDir, root)
		if err := retentionSafePath(a.dataDir, full); err != nil {
			return nil, err
		}
		if err := walk(full, 0); err != nil {
			return nil, err
		}
	}
	// Optional quota control files and their interrupted staging live beside
	// checkpoints. Count only this reserved prefix, never unrelated app data.
	control, err := os.Open(a.dataDir)
	if err != nil {
		return nil, err
	}
	for {
		children, readErr := control.ReadDir(128)
		for _, child := range children {
			if child.Name() == retentionBudgetLedgerName || strings.HasPrefix(child.Name(), retentionBudgetLedgerName+".quota-") {
				full := filepath.Join(a.dataDir, child.Name())
				if err := retentionSafePath(a.dataDir, full); err != nil {
					control.Close()
					return nil, err
				}
				if err := walk(full, 0); err != nil {
					control.Close()
					return nil, err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			control.Close()
			return nil, readErr
		}
	}
	if err := control.Close(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *StoreRetentionAudit) classifyStoreRetentionLocked(ctx context.Context) (*StoreRetentionAudit, error) {
	stores := map[string]bool{}
	for rel := range a.files {
		parts := strings.Split(rel, "/")
		if len(parts) >= 3 && validRetentionStore(parts[0]+"/"+parts[1]) {
			stores[parts[0]+"/"+parts[1]] = true
		}
	}
	names := make([]string, 0, len(stores))
	for store := range stores {
		names = append(names, store)
	}
	sort.Strings(names)
	claimed := map[string]bool{}
	for _, store := range names {
		r, recordUnits, fixedObjects, err := a.inspectRepo(ctx, store)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			a.Protected = append(a.Protected, RetentionProtection{Store: store, Reason: "incomplete or unsupported metadata/roots; all files retained"})
			continue
		}
		a.repos[store] = r
		kinds := make([]string, 0, len(r.rootCounts))
		for kind := range r.rootCounts {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			a.Roots = append(a.Roots, RetentionRoot{Store: store, Kind: kind, Count: r.rootCounts[kind]})
		}
		var graphBytes int64
		for oid := range fixedObjects {
			if info := a.files[store+"/objects.git/objects/"+oid[:2]+"/"+oid[2:]]; info != nil {
				graphBytes, err = retentionAdd(graphBytes, info.Size())
				if err != nil {
					return nil, err
				}
			}
		}
		a.Roots = append(a.Roots, RetentionRoot{Store: store, Kind: "mandatory_graph", Count: len(fixedObjects), Bytes: graphBytes})
		for _, units := range recordUnits {
			a.Inventory.Records = append(a.Inventory.Records, units)
			for _, u := range units.Units {
				claimed[u.Path] = true
			}
		}
		for rel, info := range a.files {
			prefix := store + "/objects.git/objects/"
			if !strings.HasPrefix(rel, prefix) {
				continue
			}
			oid := looseRetentionOID(strings.TrimPrefix(rel, prefix))
			if oid == "" {
				continue
			}
			proof, proofErr := retentionProveFile(filepath.Join(a.dataDir, filepath.FromSlash(rel)), info)
			if proofErr != nil {
				// Also mark graph-owned hard links fixed. Otherwise expiry could
				// credit bytes that cannot safely be physically reclaimed.
				a.Inventory.Fixed = append(a.Inventory.Fixed, RetentionUnit{rel, info.Size()})
				claimed[rel] = true
				continue
			}
			if fixedObjects[oid] {
				a.Inventory.Fixed = append(a.Inventory.Fixed, RetentionUnit{rel, info.Size()})
				claimed[rel] = true
			} else if !claimed[rel] {
				a.proofs[rel] = proof
				a.Inventory.Unreferenced = append(a.Inventory.Unreferenced, RetentionUnit{rel, info.Size()})
				claimed[rel] = true
			}
		}
	}
	for rel, info := range a.files {
		if !claimed[rel] {
			a.Inventory.Fixed = append(a.Inventory.Fixed, RetentionUnit{rel, info.Size()})
		}
	}
	sort.Slice(a.Inventory.Fixed, func(i, j int) bool { return a.Inventory.Fixed[i].Path < a.Inventory.Fixed[j].Path })
	sort.Slice(a.Inventory.Unreferenced, func(i, j int) bool { return a.Inventory.Unreferenced[i].Path < a.Inventory.Unreferenced[j].Path })
	a.Inventory.Complete = true
	a.Inventory.Evidence = a.evidence()
	return a, nil
}

func validRetentionStore(store string) bool {
	parts := strings.Split(store, "/")
	return len(parts) == 2 && (parts[0] == "checkpoints" || parts[0] == "badcheckpoints") && len(parts[1]) == 16 && validRetentionOID(parts[1], 16)
}

func validRetentionOID(s string, length int) bool {
	if len(s) != length || s != strings.ToLower(s) || strings.Trim(s, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func looseRetentionOID(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) == 2 && len(parts[0]) == 2 && len(parts[1]) == 38 && validRetentionOID(parts[0]+parts[1], 40) {
		return parts[0] + parts[1]
	}
	return ""
}

func retentionRead(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > max {
		return nil, ErrStoreInventory
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, ErrStoreInventory
	}
	return data, nil
}

func retentionSHA(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (a *StoreRetentionAudit) inspectRepo(ctx context.Context, store string) (*retentionRepo, []RetentionRecord, map[string]bool, error) {
	r := &retentionRepo{store: store, repo: filepath.Join(a.dataDir, filepath.FromSlash(store), "objects.git"), meta: filepath.Join(a.dataDir, filepath.FromSlash(store), "turns.json"), refs: map[string]string{}, owned: map[RetentionKey]map[string]string{}, rootCounts: map[string]int{}}
	data, err := a.read(r.meta, retentionMaxMetadata)
	if err != nil || json.Unmarshal(data, &r.raw) != nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil, nil, ErrStoreInventory
	}
	r.metaHash = retentionSHA(data)
	expiryPath := filepath.Join(filepath.Dir(r.meta), retentionExpiryName)
	if info := a.files[store+"/"+retentionExpiryName]; info != nil {
		r.expiryRaw, err = a.read(expiryPath, retentionMaxMetadata)
		if err != nil {
			return nil, nil, nil, err
		}
		r.expiryHash = retentionSHA(r.expiryRaw)
	}
	if _, err := retentionDecodeExpiry(r.expiryRaw); err != nil {
		return nil, nil, nil, err
	}
	seen := map[string]bool{}
	for _, raw := range r.raw {
		var record Record
		if json.Unmarshal(raw, &record) != nil || record.ID == "" || strings.TrimSpace(record.SessionID) == "" || record.UserSeq < 0 || seen[record.ID] || len(record.Files) > maxSnapshotFiles || (record.Before != "" && !validRetentionOID(record.Before, 40)) || (record.After != "" && !validRetentionOID(record.After, 40)) {
			return nil, nil, nil, ErrStoreInventory
		}
		seen[record.ID] = true
		r.records = append(r.records, record)
	}
	// Alternate/shallow/promisor/linked repository layouts need explicit offline
	// migration. Retention never assumes their complete roots or fetches objects.
	for _, rel := range []string{"objects/info/alternates", "objects/info/http-alternates", "shallow", "commondir", "info/grafts"} {
		if _, err := os.Lstat(filepath.Join(r.repo, filepath.FromSlash(rel))); err == nil || !os.IsNotExist(err) {
			return nil, nil, nil, ErrStoreInventory
		}
	}
	for rel := range a.files {
		if strings.HasPrefix(rel, store+"/objects.git/") && strings.HasSuffix(rel, ".promisor") {
			return nil, nil, nil, ErrStoreInventory
		}
	}
	format := ""
	if err := a.gitTokens(ctx, r.repo, nil, false, func(token string) error { format += token; return nil }, "rev-parse", "--show-object-format"); err != nil || format != "sha1" {
		return nil, nil, nil, ErrStoreInventory
	}
	if err := a.gitTokens(ctx, r.repo, nil, false, func(line string) error {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || !validRetentionOID(parts[1], 40) {
			return ErrStoreInventory
		}
		if parts[2] != "" {
			return ErrStoreInventory // Symbolic/aliased refs are not automatically retired.
		}
		r.refs[parts[0]] = parts[1]
		return nil
	}, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)"); err != nil {
		return nil, nil, nil, err
	}
	for name := range r.refs {
		if strings.HasPrefix(name, "refs/replace/") {
			return nil, nil, nil, ErrStoreInventory // Ref name itself identifies another object.
		}
	}
	fixedRoots := map[string]bool{}
	ownedNames := map[string]bool{}
	protectRecords := false
	for name, oid := range r.refs {
		if strings.HasPrefix(name, "refs/supercli/active/") {
			fixedRoots[oid] = true
			parts := strings.Split(strings.TrimPrefix(name, "refs/supercli/active/"), "/")
			knownBefore := len(parts) == 2 && validTurnLeaseID(parts[0]) && parts[1] == "before" && a.files[store+"/leases/"+parts[0]+".lock"] != nil
			if !knownBefore {
				protectRecords = true // Includes pending committed receipts/unknown active refs.
				r.rootCounts["active_pending_or_unknown"]++
			} else {
				r.rootCounts["active_before"]++
			}
		}
	}
	result := make([]RetentionRecord, len(r.records))
	for i, record := range r.records {
		key := RetentionKey{store, record.ID}
		result[i] = RetentionRecord{Key: key, CreatedAt: record.CreatedAt, Order: i, Protected: protectRecords}
		r.owned[key] = map[string]string{}
		for _, side := range []struct{ name, oid string }{{"before", record.Before}, {"after", record.After}} {
			if side.oid == "" {
				continue
			}
			name := recordRefRoot(record.ID) + "/" + side.name
			if r.refs[name] == side.oid {
				ownedNames[name] = true
				r.owned[key][name] = side.oid
			}
		}
		objects, err := a.graph(ctx, r.repo, []string{record.Before, record.After})
		if err != nil {
			return nil, nil, nil, err
		}
		for oid := range objects {
			rel := store + "/objects.git/objects/" + oid[:2] + "/" + oid[2:]
			if info := a.files[rel]; info != nil {
				result[i].Units = append(result[i].Units, RetentionUnit{rel, info.Size()})
			}
		}
		// Exact loose owned refs can be reclaimed with their record; packed-refs
		// remains fixed. Unknown/mismatched/latest refs always remain protected.
		for name := range r.owned[key] {
			rel := store + "/objects.git/" + name
			if info := a.files[rel]; info != nil {
				result[i].Units = append(result[i].Units, RetentionUnit{rel, info.Size()})
			}
		}
		sort.Slice(result[i].Units, func(x, y int) bool { return result[i].Units[x].Path < result[i].Units[y].Path })
	}
	for name, oid := range r.refs {
		if !ownedNames[name] {
			fixedRoots[oid] = true
			switch {
			case name == "refs/supercli/latest":
				r.rootCounts["latest"]++
			case strings.HasPrefix(name, "refs/supercli/active/"):
				// Already classified above.
			default:
				r.rootCounts["unknown_ref"]++
			}
		}
	}
	// HEAD remains protected even when it aliases a recognized record ref.
	head, err := a.read(filepath.Join(r.repo, "HEAD"), 4096)
	if err != nil {
		return nil, nil, nil, err
	}
	headValue := strings.TrimSpace(string(head))
	if strings.HasPrefix(headValue, "ref: ") {
		if oid := r.refs[strings.TrimPrefix(headValue, "ref: ")]; oid != "" {
			fixedRoots[oid] = true
			r.rootCounts["HEAD"]++
		}
	} else if validRetentionOID(headValue, 40) {
		fixedRoots[headValue] = true
		r.rootCounts["HEAD"]++
	} else {
		return nil, nil, nil, ErrStoreInventory
	}
	state := sha256.New()
	state.Write(data)
	fmt.Fprintf(state, "expiry\x00%s\n", r.expiryHash)
	refNames := make([]string, 0, len(r.refs))
	for name := range r.refs {
		refNames = append(refNames, name)
	}
	sort.Strings(refNames)
	for _, name := range refNames {
		fmt.Fprintf(state, "ref\x00%s\x00%s\n", name, r.refs[name])
	}
	for _, rel := range a.sortedFileNames() {
		prefix := store + "/objects.git/"
		if !strings.HasPrefix(rel, prefix) {
			continue
		}
		local := strings.TrimPrefix(rel, prefix)
		if local == "HEAD" || local == "config" || local == "index" || local == "packed-refs" || strings.HasPrefix(local, "refs/") || strings.HasPrefix(local, "logs/") || strings.HasPrefix(local, "sharedindex.") {
			data, err := a.read(filepath.Join(a.dataDir, filepath.FromSlash(rel)), retentionMaxGitState)
			if err != nil {
				return nil, nil, nil, err
			}
			fmt.Fprintf(state, "%s\x00%s\n", local, retentionSHA(data))
			if strings.HasPrefix(local, "logs/") {
				scan := bufio.NewScanner(bytes.NewReader(data))
				scan.Buffer(make([]byte, 4096), 128<<10)
				for scan.Scan() {
					line := scan.Text()
					if len(line) < 82 || line[40] != ' ' || line[81] != ' ' {
						return nil, nil, nil, ErrStoreInventory
					}
					for _, oid := range []string{line[:40], line[41:81]} {
						if strings.Trim(oid, "0") == "" {
							continue
						}
						if !validRetentionOID(oid, 40) {
							return nil, nil, nil, ErrStoreInventory
						}
						fixedRoots[oid] = true
						r.rootCounts["reflog_oids"]++
					}
					if err := a.countToken(); err != nil {
						return nil, nil, nil, err
					}
				}
				if scan.Err() != nil {
					return nil, nil, nil, ErrStoreInventory
				}
			}
		}
	}
	if _, exists := a.files[store+"/objects.git/index"]; exists {
		if err := a.gitTokens(ctx, r.repo, nil, true, func(token string) error {
			r.rootCounts["index_entries"]++
			header, _, ok := strings.Cut(token, "\t")
			fields := strings.Fields(header)
			if !ok || len(fields) != 3 || (!validRetentionOID(fields[1], 40) && strings.Trim(fields[1], "0") != "") {
				return ErrStoreInventory
			}
			if strings.Trim(fields[1], "0") != "" {
				fixedRoots[fields[1]] = true
			}
			return nil
		}, "ls-files", "--stage", "-z"); err != nil {
			return nil, nil, nil, err
		}
	}
	rootList := make([]string, 0, len(fixedRoots))
	for oid := range fixedRoots {
		rootList = append(rootList, oid)
	}
	fixedObjects, err := a.graph(ctx, r.repo, rootList)
	if err != nil {
		return nil, nil, nil, err
	}
	r.state = hex.EncodeToString(state.Sum(nil))
	return r, result, fixedObjects, nil
}

func (a *StoreRetentionAudit) sortedFileNames() []string {
	names := make([]string, 0, len(a.files))
	for name := range a.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *StoreRetentionAudit) evidence() string {
	h := sha256.New()
	for _, rel := range a.sortedFileNames() {
		info := a.files[rel]
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano(), info.Mode())
	}
	names := make([]string, 0, len(a.repos))
	for name := range a.repos {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(h, "%s\x00%s\n", name, a.repos[name].state)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a *StoreRetentionAudit) countToken() error {
	a.tokens++
	if a.tokens > retentionMaxTokens {
		return fmt.Errorf("%w: Git enumeration exceeds %d tokens", ErrStoreInventory, retentionMaxTokens)
	}
	return nil
}

func (a *StoreRetentionAudit) read(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Size() < 0 || info.Size() > max || a.readBytes > retentionMaxReadBytes-info.Size() {
		return nil, ErrStoreInventory
	}
	a.readBytes += info.Size()
	return retentionRead(path, max)
}

func retentionGitCommand(ctx context.Context, repo string, args ...string) *exec.Cmd {
	base := []string{"--git-dir=" + repo, "-c", "core.longpaths=true", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "core.hooksPath=" + os.DevNull}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = repo
	cmd.Env = append(checkpointGitEnv(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_INDEX_FILE="+filepath.Join(repo, "index"))
	cmd.Stderr = io.Discard
	configureCheckpointCommand(cmd)
	return cmd
}

func (a *StoreRetentionAudit) gitTokens(ctx context.Context, repo string, input io.Reader, nul bool, visit func(string) error, args ...string) error {
	a.commands++
	if a.commands > retentionMaxCommands {
		return ErrStoreInventory
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := retentionGitCommand(childCtx, repo, args...)
	cmd.Stdin = input
	stream, err := startCheckpointCommandStream(childCtx, cmd)
	if err != nil {
		return errors.Join(ErrStoreInventory, ctx.Err(), err)
	}
	stdout := stream.Stdout
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), 128<<10)
	if nul {
		scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := bytes.IndexByte(data, 0); i >= 0 {
				return i + 1, data[:i], nil
			}
			if atEOF && len(data) != 0 {
				return 0, nil, ErrStoreInventory
			}
			return 0, nil, nil
		})
	}
	var readErr error
	for scan.Scan() {
		if readErr = a.countToken(); readErr == nil {
			readErr = visit(scan.Text())
		}
		if readErr != nil {
			cancel()
			break
		}
	}
	if readErr == nil {
		readErr = scan.Err()
		if readErr != nil {
			cancel()
		}
	}
	waitErr := stream.Wait()
	if readErr != nil || waitErr != nil {
		return errors.Join(ErrStoreInventory, ctx.Err(), readErr, waitErr)
	}
	return nil
}

func (a *StoreRetentionAudit) graph(ctx context.Context, repo string, roots []string) (map[string]bool, error) {
	input := strings.Builder{}
	seen := map[string]bool{}
	for _, oid := range roots {
		if oid != "" && !seen[oid] {
			if !validRetentionOID(oid, 40) {
				return nil, ErrStoreInventory
			}
			seen[oid] = true
			input.WriteString(oid + "\n")
		}
	}
	result := map[string]bool{}
	if input.Len() == 0 {
		return result, nil
	}
	err := a.gitTokens(ctx, repo, strings.NewReader(input.String()), false, func(oid string) error {
		if !validRetentionOID(oid, 40) {
			return ErrStoreInventory
		}
		result[oid] = true
		return nil
	}, "rev-list", "--objects", "--no-object-names", "--stdin")
	return result, err
}
