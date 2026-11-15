package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
	"supercli/internal/tools/search"
)

// EditFiles applies ONE edit (replace, prepend or append) to every file that
// matches a glob, with content-based skip/only filters and a dry run. It is
// the many-file counterpart of patch_file: a rename across a tree, a license
// header on every source file, an import added everywhere. Each written file
// is replaced atomically under the shared mutation queue; the result lists
// every changed, skipped and unmatched file, and a replace reports how many
// matches are left in the included files, so completeness is checked by the
// same call that made the change.
type EditFiles struct {
	BaseDir string
	// ApprovalThreshold is the number of files above which an apply asks the
	// host approval gate first (a mass edit). 0 = editFilesApprovalThreshold.
	ApprovalThreshold int
}

const (
	// editFilesApprovalThreshold: more files than this in one apply is a mass
	// edit and goes through the approval gate, like other bulk operations.
	editFilesApprovalThreshold = 200
	// editFilesMaxFiles caps the files one call may change.
	editFilesMaxFiles = 1000
	// editFilesMaxScan caps the files one call may inspect.
	editFilesMaxScan = 20000
	// editFilesMaxBytes skips very large files (generated blobs, data dumps).
	editFilesMaxBytes = 4 << 20
	// editFilesListCap bounds each file list in the result text.
	editFilesListCap = 120
)

// NewEditFiles returns an EditFiles tool rooted at baseDir.
func NewEditFiles(baseDir string) *EditFiles {
	return &EditFiles{BaseDir: baseDir}
}

type editFilesArgs struct {
	Path          string  `json:"path"`
	Include       string  `json:"include"`
	Exclude       string  `json:"exclude"`
	SkipIfMatches string  `json:"skip_if_matches"`
	OnlyIfMatches string  `json:"only_if_matches"`
	Action        string  `json:"action"`
	Old           string  `json:"old"`
	New           *string `json:"new"`
	Regex         bool    `json:"regex"`
	Word          bool    `json:"word"`
	DryRun        bool    `json:"dry_run"`
}

// Spec returns the tool definition.
func (t *EditFiles) Spec() Tool {
	return Tool{
		Name: "edit_files",
		Description: "Apply one edit to MANY files matching a glob, in one call: replace text (rename), or prepend/append text (header, import). " +
			"Filters: exclude glob, skip_if_matches / only_if_matches (regex on content). " +
			"Result lists changed/skipped files and, for replace, matches left. dry_run previews without writing.",
		Schema: `{
			"type": "object",
			"properties": {
				"include": {"type": "string", "description": "Glob relative to path, e.g. **/*.go or src/**/*.{ts,tsx}"},
				"path": {"type": "string", "description": "Folder to search (default: project folder)"},
				"exclude": {"type": "string", "description": "Glob of files to leave alone, e.g. **/testdata/**"},
				"action": {"type": "string", "enum": ["replace", "prepend", "append"], "description": "Default replace"},
				"old": {"type": "string", "description": "replace: text to find (literal unless regex)"},
				"new": {"type": "string", "description": "Replacement, or the text to prepend/append"},
				"word": {"type": "boolean", "description": "replace: old matches whole identifiers only"},
				"regex": {"type": "boolean", "description": "replace: old is a Go regex; new may use $1"},
				"skip_if_matches": {"type": "string", "description": "Regex; files whose content matches are not edited"},
				"only_if_matches": {"type": "string", "description": "Regex; only files whose content matches are edited"},
				"dry_run": {"type": "boolean", "description": "Preview only; write nothing"}
			},
			"required": ["include", "new"],
			"additionalProperties": false
		}`,
		Fn:       t.execute,
		Approval: t.approval,
	}
}

// editFilesPlan is the computed effect of one call: new contents for every
// file that changes, plus the bookkeeping the result reports.
type editFilesPlan struct {
	root         string
	changes      []editFilesChange
	skipped      []string // skip_if_matches / only_if_matches
	unmatched    []string // replace found nothing
	unreadable   []string // binary, too large or unreadable
	replacements int
	remaining    int // replace: matches still present in included files
	scanLimited  bool
}

type editFilesChange struct {
	full, rel string
	before    []byte
	after     []byte
	count     int
}

func (a *editFilesArgs) normalize() error {
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	if a.Action == "" {
		a.Action = "replace"
	}
	if a.New == nil {
		return fmt.Errorf("new is required (the replacement, or the text to insert)")
	}
	switch a.Action {
	case "replace":
		if a.Old == "" {
			return fmt.Errorf("replace needs old (the text to find); to insert text use action prepend or append")
		}
		if a.Regex && a.Word {
			return fmt.Errorf("use either regex or word, not both (in a regex write \\b yourself)")
		}
	case "prepend", "append":
		if a.Old != "" || a.Regex || a.Word {
			return fmt.Errorf("%s inserts new as-is; old, regex and word apply only to replace", a.Action)
		}
		if *a.New == "" {
			return fmt.Errorf("%s needs non-empty new text", a.Action)
		}
	default:
		return fmt.Errorf("action must be replace, prepend or append")
	}
	if strings.TrimSpace(a.Include) == "" {
		return fmt.Errorf("include is required, e.g. **/*.go")
	}
	return nil
}

// plan resolves, filters and computes every new file content without writing.
func (t *EditFiles) plan(ctx context.Context, a *editFilesArgs) (*editFilesPlan, error) {
	if err := a.normalize(); err != nil {
		return nil, err
	}
	rootRel := a.Path
	if strings.TrimSpace(rootRel) == "" {
		rootRel = "."
	}
	root, err := sandbox.ResolveSafe(t.BaseDir, rootRel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fileops.FileErr(err, rootRel)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file; give its folder as path and the file name as include", rootRel)
	}
	include, err := search.CompileGlob(strings.TrimSpace(a.Include))
	if err != nil {
		return nil, err
	}
	var exclude *search.Glob
	if strings.TrimSpace(a.Exclude) != "" {
		if exclude, err = search.CompileGlob(strings.TrimSpace(a.Exclude)); err != nil {
			return nil, fmt.Errorf("exclude: %w", err)
		}
	}
	skipRe, err := compileContentRegex("skip_if_matches", a.SkipIfMatches)
	if err != nil {
		return nil, err
	}
	onlyRe, err := compileContentRegex("only_if_matches", a.OnlyIfMatches)
	if err != nil {
		return nil, err
	}
	var matcher *regexp.Regexp
	if a.Action == "replace" {
		switch {
		case a.Regex:
			if matcher, err = regexp.Compile("(?m)" + a.Old); err != nil {
				return nil, fmt.Errorf("old is not a valid regex: %w", err)
			}
		case a.Word:
			matcher = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_$])(` + regexp.QuoteMeta(a.Old) + `)(?:$|[^\p{L}\p{N}_$])`)
		}
	}

	p := &editFilesPlan{root: root}
	scanned := 0
	errScanLimit := errors.New("scan limit")
	walkErr := search.WalkFilesFiltered(ctx, root, func(dir string) bool { return include.CanDescend(root, dir) }, func(full string) error {
		if !include.Match(root, full) || (exclude != nil && exclude.Match(root, full)) {
			return nil
		}
		scanned++
		if scanned > editFilesMaxScan {
			p.scanLimited = true
			return errScanLimit
		}
		rel := relSlash(root, full)
		st, statErr := os.Stat(full)
		if statErr != nil || st.Size() > editFilesMaxBytes {
			p.unreadable = append(p.unreadable, rel)
			return nil
		}
		if fileops.EnsureTextFile(full) != nil {
			p.unreadable = append(p.unreadable, rel)
			return nil
		}
		before, readErr := os.ReadFile(full)
		if readErr != nil {
			p.unreadable = append(p.unreadable, rel)
			return nil
		}
		if (skipRe != nil && skipRe.Match(before)) || (onlyRe != nil && !onlyRe.Match(before)) {
			p.skipped = append(p.skipped, rel)
			return nil
		}
		after, count := applyEditFiles(before, a, matcher)
		if count == 0 || bytes.Equal(after, before) {
			p.unmatched = append(p.unmatched, rel)
			return nil
		}
		p.changes = append(p.changes, editFilesChange{full: full, rel: rel, before: before, after: after, count: count})
		p.replacements += count
		if a.Action == "replace" {
			_, left := applyEditFiles(after, a, matcher)
			p.remaining += left
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errScanLimit) {
		return nil, walkErr
	}
	if len(p.changes) > editFilesMaxFiles {
		return nil, fmt.Errorf("%d files would change, more than the %d-file limit of one call; narrow include/path", len(p.changes), editFilesMaxFiles)
	}
	sort.Slice(p.changes, func(i, j int) bool { return p.changes[i].rel < p.changes[j].rel })
	sort.Strings(p.skipped)
	sort.Strings(p.unmatched)
	sort.Strings(p.unreadable)
	return p, nil
}

func compileContentRegex(name, expr string) (*regexp.Regexp, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, nil
	}
	re, err := regexp.Compile("(?m)" + expr)
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid regex: %w", name, err)
	}
	return re, nil
}

func relSlash(root, full string) string {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return filepath.ToSlash(full)
	}
	return filepath.ToSlash(rel)
}

// applyEditFiles computes the edited content and the number of edits. Text
// given with \n follows the file's own line endings (CRLF files stay CRLF),
// and a UTF-8 byte-order mark stays first.
func applyEditFiles(content []byte, a *editFilesArgs, matcher *regexp.Regexp) ([]byte, int) {
	crlf := bytes.Contains(content, []byte("\r\n"))
	eol := func(s string) string {
		if !crlf {
			return s
		}
		return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	}
	text := *a.New
	switch a.Action {
	case "prepend":
		ins := eol(text)
		if !strings.HasSuffix(ins, "\n") {
			ins += eol("\n")
		}
		bom := []byte{}
		body := content
		if bytes.HasPrefix(content, []byte("\xEF\xBB\xBF")) {
			bom, body = content[:3], content[3:]
		}
		out := make([]byte, 0, len(content)+len(ins))
		out = append(append(append(out, bom...), ins...), body...)
		return out, 1
	case "append":
		ins := eol(text)
		out := append([]byte{}, content...)
		if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, eol("\n")...)
		}
		return append(out, ins...), 1
	}
	switch {
	case a.Regex:
		n := len(matcher.FindAllIndex(content, -1))
		if n == 0 {
			return content, 0
		}
		return matcher.ReplaceAll(content, []byte(eol(text))), n
	case a.Word:
		return replaceWord(content, matcher, eol(text))
	default:
		old := eol(a.Old)
		n := bytes.Count(content, []byte(old))
		if n == 0 {
			return content, 0
		}
		return bytes.ReplaceAll(content, []byte(old), []byte(eol(text))), n
	}
}

// replaceWord replaces submatch 1 of every whole-identifier occurrence.
// Matches are found one at a time so adjacent occurrences sharing a boundary
// character are all replaced.
func replaceWord(content []byte, matcher *regexp.Regexp, repl string) ([]byte, int) {
	var out []byte
	n, pos, last := 0, 0, 0
	for pos <= len(content) {
		loc := matcher.FindSubmatchIndex(content[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[2], pos+loc[3]
		out = append(out, content[last:start]...)
		out = append(out, repl...)
		last, pos = end, end
		n++
	}
	if n == 0 {
		return content, 0
	}
	return append(out, content[last:]...), n
}

// approval sends a mass edit (more than ApprovalThreshold files) or an edit
// rooted outside the workspace (reachable only in unsandboxed mode) through
// the host approval gate. Ordinary workspace edits stay ungated, exactly like
// patch_file; a dry run never asks.
func (t *EditFiles) approval(ctx context.Context, args json.RawMessage) (*core.ApprovalRequest, error) {
	var a editFilesArgs
	if json.Unmarshal(args, &a) != nil || a.DryRun {
		return nil, nil
	}
	rootRel := a.Path
	if strings.TrimSpace(rootRel) == "" {
		rootRel = "."
	}
	outside := false
	if _, err := sandbox.ResolveWithin(t.BaseDir, rootRel); err != nil {
		if _, err := sandbox.ResolveSafe(t.BaseDir, rootRel); err != nil {
			return nil, nil // execute reports the sandbox refusal
		}
		outside = true
	}
	threshold := t.ApprovalThreshold
	if threshold <= 0 {
		threshold = editFilesApprovalThreshold
	}
	p, err := t.plan(ctx, &a)
	if err != nil {
		return nil, nil // execute reports the argument error
	}
	if !outside && len(p.changes) <= threshold {
		return nil, nil
	}
	op := "mass_edit"
	summary := fmt.Sprintf("Edit %d files under %s (%s %s)? Each file is changed in place; the turn can be undone.", len(p.changes), p.root, a.Action, a.Include)
	if outside {
		op = "edit_outside_workspace"
		summary = fmt.Sprintf("Edit %d files under %s, which is OUTSIDE the workspace (%s %s)?", len(p.changes), p.root, a.Action, a.Include)
	}
	return &core.ApprovalRequest{
		Tool: "edit_files", Operation: op, Risk: core.RiskWriteReversible, Reversible: !outside,
		Target:  p.root,
		Summary: summary,
		Scope:   []string{"edit_files", op, "filesystem", p.root},
		Count:   len(p.changes),
	}, nil
}

func (t *EditFiles) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a editFilesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("edit_files: bad args: %w", err)}, nil
	}
	p, err := t.plan(ctx, &a)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_files: %w", err)}, nil
	}
	if a.DryRun || len(p.changes) == 0 {
		return Result{Text: p.render(&a, true, nil, 0)}, nil
	}
	paths := make([]string, len(p.changes))
	for i, c := range p.changes {
		paths[i] = c.full
	}
	release, err := fileops.LockMutationPathsContext(ctx, paths...)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_files: %w", err)}, nil
	}
	defer release()
	var written []string
	var writeErr error
	for _, c := range p.changes {
		if err := ctx.Err(); err != nil {
			writeErr = err
			break
		}
		// The plan read each file without the lock: refuse a file that
		// changed since, rather than overwrite someone else's edit.
		now, err := os.ReadFile(c.full)
		if err != nil || !bytes.Equal(now, c.before) {
			writeErr = fmt.Errorf("%s changed while edit_files was running; nothing more written", c.rel)
			break
		}
		if err := writeFileAtomic(c.full, c.after); err != nil {
			writeErr = fmt.Errorf("%s: %w", c.rel, err)
			break
		}
		written = append(written, c.full)
	}
	res := Result{Text: p.render(&a, false, writeErr, len(written)), ChangedPaths: written}
	if writeErr != nil && len(written) == 0 {
		res.Err = fmt.Errorf("edit_files: %w", writeErr)
		res.Text = ""
	}
	return res, nil
}

// writeFileAtomic replaces path through a sibling temp file, keeping the mode.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".edit-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name)
	}
	return werr
}

func (p *editFilesPlan) render(a *editFilesArgs, preview bool, writeErr error, written int) string {
	var b strings.Builder
	verb := "changed"
	if preview && a.DryRun {
		verb = "would change"
	}
	n := len(p.changes)
	if !preview {
		n = written
	}
	fmt.Fprintf(&b, "edit_files %s: %s %d file(s)", a.Action, verb, n)
	if a.Action == "replace" {
		fmt.Fprintf(&b, ", %d replacement(s)", p.replacements)
	}
	fmt.Fprintf(&b, "; skipped by filter %d; no match %d; not text/too large %d.", len(p.skipped), len(p.unmatched), len(p.unreadable))
	if writeErr != nil {
		fmt.Fprintf(&b, "\nSTOPPED after %d of %d files: %v", written, len(p.changes), writeErr)
	}
	if p.scanLimited {
		fmt.Fprintf(&b, "\n[scan limit %d files reached: results incomplete; narrow path/include]", editFilesMaxScan)
	}
	list := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s:", title)
		for i, it := range items {
			if i == editFilesListCap {
				fmt.Fprintf(&b, " ... (+%d more)", len(items)-i)
				break
			}
			b.WriteString(" " + it)
		}
	}
	rels := make([]string, 0, len(p.changes))
	for i, c := range p.changes {
		if !preview && i >= written {
			break
		}
		rels = append(rels, c.rel)
	}
	list(verb, rels)
	list("skipped by skip_if_matches/only_if_matches", p.skipped)
	if a.Action == "replace" {
		list("no match", p.unmatched)
	}
	list("not text or too large", p.unreadable)
	if a.Action == "replace" && (writeErr == nil) && len(p.changes) > 0 {
		fmt.Fprintf(&b, "\nmatches of old left in edited files: %d", p.remaining)
	}
	if preview && a.DryRun && len(p.changes) > 0 {
		c := p.changes[0]
		fmt.Fprintf(&b, "\npreview %s:\n%s", c.rel, firstDiff(c.before, c.after))
	}
	if len(p.changes) == 0 && !a.DryRun {
		b.WriteString("\nNothing written.")
	}
	return b.String()
}

// firstDiff renders the first differing lines of one file (a small preview).
func firstDiff(before, after []byte) string {
	bl := strings.Split(strings.ReplaceAll(string(before), "\r\n", "\n"), "\n")
	al := strings.Split(strings.ReplaceAll(string(after), "\r\n", "\n"), "\n")
	i := 0
	for i < len(bl) && i < len(al) && bl[i] == al[i] {
		i++
	}
	var b strings.Builder
	for k := i; k < len(bl) && k < i+3; k++ {
		if k < len(al) && bl[k] == al[k] {
			break
		}
		fmt.Fprintf(&b, "- %s\n", bl[k])
	}
	for k := i; k < len(al) && k < i+4; k++ {
		fmt.Fprintf(&b, "+ %s\n", al[k])
	}
	return strings.TrimRight(b.String(), "\n")
}
