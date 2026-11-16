package agent

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"supercli/internal/tools/search"
)

// Name check ("verify before use"): a request often names things it assumes
// exist — a config key, a function, a file, a CLI flag. When one of them does
// not exist, models tend to invent it (add the key and wire it up) or apply
// the change to a similar-looking name instead. At the start of a coordinator
// Run the code-like names of the request are looked up in the workspace with
// one bounded scan; the names that appear nowhere are listed for the model in
// the request tail, with the rule to report them instead of inventing or
// substituting them. Names the request asks to create ("add a `--verbose`
// flag") are not checked. The check is generic (no task knowledge), costs no
// model call, adds nothing when every name exists, and stays silent when the
// scan cannot cover the workspace (too many files or too slow), because
// "not found" would then be a guess. Config name_check = false turns it off.

const (
	nameCheckMaxNames    = 12
	nameCheckMaxFiles    = 4000
	nameCheckMaxFileSize = 512 << 10
	nameCheckBudget      = 400 * time.Millisecond
	nameCheckMaxIdents   = 200000
)

var (
	nameCheckBacktick = regexp.MustCompile("`([^`\n]{1,120})`")
	// Unquoted code-like tokens: snake_case, SCREAMING_SNAKE, camelCase,
	// call syntax name(), --flags and paths with a file extension.
	nameCheckSnake = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b`)
	nameCheckCamel = regexp.MustCompile(`\b[a-z][a-z0-9]*[A-Z][A-Za-z0-9]*\b`)
	nameCheckCall  = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_.]*)\(\)`)
	nameCheckFlag  = regexp.MustCompile(`(?:^|[\s(])(--[a-z][a-z0-9-]+)\b`)
	nameCheckPath  = regexp.MustCompile(`(?:^|[\s(])((?:[A-Za-z0-9_.-]+[/\\])*[A-Za-z0-9_-][A-Za-z0-9_.-]*\.(?:go|py|ts|tsx|js|jsx|mjs|cjs|json|toml|ya?ml|ini|cfg|conf|env|md|txt|rs|java|kt|c|h|cc|cpp|hpp|cs|rb|php|sh|ps1|bat|sql|html|css|scss|xml|proto|gradle|lock|mod|sum|csv))\b`)
	nameCheckURL   = regexp.MustCompile(`(?i)\b(?:https?|ftp|file)://\S+|\bwww\.\S+`)
	nameCheckIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	// Fenced or indented code blocks.
	nameCheckFence  = regexp.MustCompile("(?s)```.*?(?:```|$)|(?m)^(?: {4}|\t)[^\n]*$")
	nameCheckClause = regexp.MustCompile(`(?i)[.!?;:,]\s|\n|\s(?:and|then|also|plus|i|oraz|potem|a\s+tak[zż]e)\s`)
	// Words before a name that make it a name to create or an output file.
	nameCheckCreate = regexp.MustCompile(`(?i)\b(?:add|adds|adding|create|creates|creating|introduce|introducing|new|define|defining|implement|implementing|generate|write|writes|build|save|keep|append|store|output|produce|export|dump|record|dodaj\w*|dopisz\w*|utw[oó]rz\w*|stw[oó]rz\w*|now[aey]\w*|zdefiniuj\w*|zaimplementuj\w*|napisz\w*|zapisz\w*|zbuduj\w*|wygeneruj\w*|eksportuj\w*|(?:a|an)\s+(?:[\w-]+\s+){0,3}(?:named|called))\b`)
	// A rename/move/split clause whose target follows "to"/"into"/"na".
	nameCheckRenameVerb = regexp.MustCompile(`(?i)\b(?:renam\w*|mov(?:e|es|ing)|split\w*|extract\w*|migrat\w*|convert\w*|przemianuj\w*|zmie[nń]\s+nazw\w*|przenie[sś]\w*|wydziel\w*)\b`)
	nameCheckRenameTo   = regexp.MustCompile(`(?i)\b(?:to|into|as|na|do)\s*` + "[`'\"]?$")
	// A value change whose new value follows "to"/"na": "change the region
	// from `westeurope` to `polandcentral`", "set `mode` to `strict`",
	// "raise X to Y". The new value is the user's, not an existing name.
	nameCheckChangeVerb = regexp.MustCompile(`(?i)\b(?:chang\w*|set|sets|setting|from|updat\w*|bump\w*|rais\w*|lower\w*|increas\w*|decreas\w*|switch\w*|replac\w*|modif\w*|turn\w*|zmie[nń]\w*|ustaw\w*|podnie[sś]\w*|obni[zż]\w*|zwi[eę]ksz\w*|zmniejsz\w*|prze[lł][aą]cz\w*|zast[aą]p\w*)(?:[^\pL\pN_]|$)`)
	nameCheckChangeTo   = regexp.MustCompile(`(?i)\b(?:to|na)\s*` + "[`'\"]?$")
	// A list whose header line asks to create or split into new things:
	// "Split `util` into three packages:\n- `internal/textutil` ...".
	nameCheckListItem = regexp.MustCompile(`^\s*(?:[-*•]|\(?[0-9a-z]{1,2}[.)])\s`)
	// Only the list marker precedes the name: the item starts with it.
	nameCheckListLead = regexp.MustCompile(`^\s*(?:[-*•]|\(?[0-9a-z]{1,2}[.)])\s+$`)
	nameCheckIntoNew  = regexp.MustCompile(`(?i)\b(?:into|na|do)\b`)
	// Words near a slash name or a bare word that make it a git ref
	// ("create `release/1.4` from `main`", "rebase `feature/x`").
	nameCheckGitWords = regexp.MustCompile(`(?i)\b(?:branch\w*|rebas\w*|merg\w*|checkout|check\s+out|cherry-pick\w*|git|upstream|ga[lł][eę]\w*|ga[lł][aą][zź]\w*)\b`)
	// Words before a backticked plain word that make it a literal value
	// ("the line `none`", "exactly `END`", "ends with `Admin`").
	nameCheckValue = regexp.MustCompile(`(?i)\b(?:exactly|like|line|string|text|literal|word|value|label|marker|status|e\.g\.|such as|form|print\w*|return\w*|end(?:s|ing)?\s+with|start(?:s|ing)?\s+with|suffix|prefix|contain\w*|match\w*|pattern|dokładnie|np\.|lini[ai]|tekst\w*|warto[sś]\w*|napis\w*|ko[nń]cz\w*\s+si[eę]\s+na|zaczyna\w*\s+si[eę]\s+od)\b`)
)

// nameCheckCandidate is one name the request treats as existing.
type nameCheckCandidate struct {
	name string // as written in the request
	path bool   // looks like a file path
	// needles are the strings that must appear in the workspace (one is
	// enough for a path, all of them for a dotted name).
	needles []string
}

// requestNameCandidates extracts the code-like names of prompt that are not
// introduced as new. A miss only means the name is not checked.
func requestNameCandidates(prompt string) []nameCheckCandidate {
	blank := func(s string) string { return strings.Repeat(" ", len(s)) }
	// Pasted code blocks may hold names the user proposes; URLs are not names.
	text := nameCheckURL.ReplaceAllStringFunc(nameCheckFence.ReplaceAllStringFunc(prompt, blank), blank)
	// Names shown in a code block (an expected JSON shape, a snippet) are
	// the user's own; do not check them elsewhere in the request either.
	fenced := map[string]bool{}
	for _, block := range nameCheckFence.FindAllString(prompt, -1) {
		for _, id := range nameCheckIdent.FindAllString(block, -1) {
			fenced[id] = true
		}
	}
	seen := map[string]bool{}
	var out []nameCheckCandidate
	add := func(raw string, at int, quoted bool) {
		if len(out) >= nameCheckMaxNames {
			return
		}
		c, ok := nameCheckParse(raw, quoted)
		if !ok || seen[c.name] {
			return
		}
		seen[c.name] = true
		if fenced[c.name] || introducedAsNew(text, at) || (quoted && literalValue(text, at, c)) {
			return
		}
		if nameCheckBuiltin(c) || nameCheckPlaceholder(c) || gitRefLike(text, at, c) {
			return
		}
		if c.path && (at > 0 && text[at-1] == '*' || strings.HasPrefix(c.needles[0], "_")) {
			return // a glob such as *_test.go
		}
		out = append(out, c)
	}
	for _, m := range nameCheckBacktick.FindAllStringSubmatchIndex(text, -1) {
		add(text[m[2]:m[3]], m[0], true)
	}
	// Unquoted tokens: blank the backticked spans first so they are not
	// parsed twice.
	plain := nameCheckBacktick.ReplaceAllStringFunc(text, blank)
	type hit struct {
		at  int
		raw string
	}
	var hits []hit
	for _, re := range []*regexp.Regexp{nameCheckPath, nameCheckFlag, nameCheckCall} {
		for _, m := range re.FindAllStringSubmatchIndex(plain, -1) {
			hits = append(hits, hit{m[2], plain[m[2]:m[3]]})
		}
	}
	for _, re := range []*regexp.Regexp{nameCheckSnake, nameCheckCamel} {
		for _, m := range re.FindAllStringIndex(plain, -1) {
			hits = append(hits, hit{m[0], plain[m[0]:m[1]]})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	covered := -1
	for _, h := range hits {
		if h.at < covered {
			continue // part of a longer token already taken (a path, a call)
		}
		covered = h.at + len(h.raw)
		add(h.raw, h.at, false)
	}
	return out
}

func nameCheckParse(raw string, quoted bool) (nameCheckCandidate, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "()")
	s = strings.Trim(s, `"'.,:;`)
	if s == "" || len(s) > 100 {
		return nameCheckCandidate{}, false
	}
	if strings.ContainsAny(s, " \t") {
		// Code snippets and commands are not names.
		return nameCheckCandidate{}, false
	}
	if nameCheckPath.MatchString(" "+s) && nameCheckPath.FindStringSubmatch(" " + s)[1] == s {
		return nameCheckCandidate{name: s, path: true, needles: []string{filepath.Base(filepath.FromSlash(strings.ReplaceAll(s, `\`, "/")))}}, true
	}
	if strings.HasPrefix(s, "--") || (quoted && strings.HasPrefix(s, "-")) {
		flag := strings.TrimLeft(s, "-")
		if len(flag) < 2 || !nameCheckIdentLike(flag) {
			return nameCheckCandidate{}, false
		}
		return nameCheckCandidate{name: s, needles: []string{flag}}, true
	}
	s = strings.TrimPrefix(s, "$")
	var parts []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == ':' || r == '/' || r == '[' || r == ']' }) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nameCheckCandidate{}, false
	}
	for _, p := range parts {
		if !nameCheckIdentLike(p) {
			return nameCheckCandidate{}, false
		}
	}
	if !quoted && len(parts) == 1 && !strings.Contains(s, "_") && strings.ToLower(s) == s {
		return nameCheckCandidate{}, false
	}
	// A plain all-caps word (JSON, API, UNKNOWN) is an acronym or a value.
	if len(parts) == 1 && !strings.Contains(s, "_") && strings.ToUpper(s) == s {
		return nameCheckCandidate{}, false
	}
	// A short plain word in backticks (`cache`, `id`) is too generic to say
	// anything about when it is missing.
	if len(parts) == 1 && len(parts[0]) < 3 {
		return nameCheckCandidate{}, false
	}
	return nameCheckCandidate{name: s, needles: parts}, true
}

func nameCheckIdentLike(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// introducedAsNew reports whether the words just before position at (same
// sentence) ask to create the name.
func introducedAsNew(text string, at int) bool {
	if nameCheckCreate.MatchString(currentClause(text[max(0, at-48):at])) {
		return true
	}
	// "rename X to Y": Y is the new name.
	before := currentClause(text[max(0, at-96):at])
	trimmed := strings.TrimRight(before, " 	")
	if nameCheckRenameTo.MatchString(trimmed) && nameCheckRenameVerb.MatchString(before) {
		return true
	}
	// "change X from A to B", "set X to B", "raise X to B": B is the new value.
	if nameCheckChangeTo.MatchString(trimmed) && nameCheckChangeVerb.MatchString(before) {
		return true
	}
	return introducedByListHeader(text, at)
}

// introducedByListHeader reports whether position at is in a list item whose
// header line (the nearest line above that is not a list item) ends with ":"
// and asks to create something or to split/move/rename into new things:
// "Split `util` into three packages:\n- `internal/textutil` ...".
func introducedByListHeader(text string, at int) bool {
	lineStart := strings.LastIndexByte(text[:at], '\n') + 1
	if !nameCheckListLead.MatchString(text[lineStart:at]) {
		return false
	}
	end := lineStart - 1
	for end > 0 {
		start := strings.LastIndexByte(text[:end], '\n') + 1
		line := text[start:end]
		if strings.TrimSpace(line) != "" && !nameCheckListItem.MatchString(line) {
			header := strings.TrimRight(line, " \t\r")
			if !strings.HasSuffix(header, ":") {
				return false
			}
			if nameCheckCreate.MatchString(header) {
				return true
			}
			return nameCheckRenameVerb.MatchString(header) && nameCheckIntoNew.MatchString(header)
		}
		end = start - 1
	}
	return false
}

// nameCheckBuiltins are language builtins and standard-library names a
// request names without them having to appear in the workspace (`ValueError`,
// `Promise`, `nil`). Dotted names whose first part is a standard module
// (`os.path.join`, `JSON.parse`, `fmt.Errorf`) are skipped as well.
var nameCheckBuiltins = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields(`
		ValueError TypeError KeyError IndexError AttributeError RuntimeError Exception BaseException
		NotImplementedError NotImplemented StopIteration StopAsyncIteration OSError IOError EnvironmentError
		FileNotFoundError FileExistsError PermissionError IsADirectoryError NotADirectoryError TimeoutError
		ConnectionError ConnectionResetError ConnectionRefusedError BrokenPipeError InterruptedError
		ZeroDivisionError ArithmeticError OverflowError FloatingPointError AssertionError ImportError
		ModuleNotFoundError NameError UnboundLocalError LookupError UnicodeError UnicodeDecodeError
		UnicodeEncodeError RecursionError MemoryError SystemExit KeyboardInterrupt GeneratorExit
		SyntaxError IndentationError Warning DeprecationWarning UserWarning RuntimeWarning
		None True False self cls int float str bytes bool dict list tuple set frozenset object
		len range print isinstance issubclass enumerate zip sorted reversed open super property
		staticmethod classmethod dataclass dataclasses Decimal datetime timedelta Optional Union
		Any Callable Iterable Iterator Sequence Mapping Path pathlib
		Error EvalError RangeError ReferenceError URIError AggregateError Promise Array Object
		String Number Boolean Symbol BigInt Map Set WeakMap WeakSet Date RegExp JSON Math Intl
		console process window document globalThis undefined null NaN Infinity async await
		parseInt parseFloat setTimeout setInterval clearTimeout fetch require module exports
		nil error string rune byte uint uint8 uint16 uint32 uint64 int8 int16 int32 int64
		float32 float64 complex64 complex128 uintptr iota append cap make new panic recover
		close copy delete
		Throwable IllegalArgumentException IllegalStateException NullPointerException
		RuntimeException IOException ArgumentException ArgumentNullException
		InvalidOperationException NotImplementedException NotSupportedException
	`) {
		m[s] = true
	}
	return m
}()

var nameCheckStdModules = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields(`
		os sys re json math datetime typing collections itertools functools subprocess shutil
		pathlib logging unittest asyncio hashlib base64 uuid
		fmt strings strconv errors bufio filepath ioutil regexp slices utf8
		JSON Math Object Array Promise Number Date console process Reflect Intl Buffer
	`) {
		m[s] = true
	}
	return m
}()

func nameCheckBuiltin(c nameCheckCandidate) bool {
	if c.path || strings.HasPrefix(c.name, "-") || len(c.needles) == 0 {
		return false
	}
	if len(c.needles) == 1 {
		return nameCheckBuiltins[c.needles[0]]
	}
	return nameCheckStdModules[c.needles[0]] && !strings.Contains(c.name, "/")
}

// nameCheckPlaceholder reports whether a colon name is an output-format
// placeholder (`path:line`, `file:line:col`, `key:value`), not a name.
func nameCheckPlaceholder(c nameCheckCandidate) bool {
	if c.path || !strings.Contains(c.name, ":") || strings.Contains(c.name, "::") {
		return false
	}
	for _, p := range c.needles {
		for _, r := range p {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	return true
}

// gitRefLike reports whether a candidate is used as a git branch or ref:
// a slash name without a file extension, or a bare word such as `main`, in
// a sentence about branches, merges or rebases.
func gitRefLike(text string, at int, c nameCheckCandidate) bool {
	if c.path || strings.HasPrefix(c.name, "-") {
		return false
	}
	bare := false
	switch c.name {
	case "main", "master", "develop", "development", "trunk", "HEAD", "origin", "upstream":
		bare = true
	}
	if !strings.Contains(c.name, "/") && !bare {
		return false
	}
	start := max(0, at-120)
	end := min(len(text), at+len(c.name)+80)
	return nameCheckGitWords.MatchString(text[start:end])
}

// gitRefNames returns the short names of the branches, remote branches and
// tags of the git repository at root (nil when there is none). Read-only and
// bounded: loose refs under .git/refs plus packed-refs.
func gitRefNames(root string) map[string]bool {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		data, rerr := os.ReadFile(gitDir)
		if rerr != nil {
			return nil
		}
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "gitdir:") {
			return nil
		}
		gitDir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
		if common, cerr := os.ReadFile(filepath.Join(gitDir, "commondir")); cerr == nil {
			c := strings.TrimSpace(string(common))
			if !filepath.IsAbs(c) {
				c = filepath.Join(gitDir, c)
			}
			gitDir = c
		}
	}
	refs := map[string]bool{}
	addRef := func(full string) {
		full = strings.TrimSpace(full)
		for _, prefix := range []string{"refs/heads/", "refs/tags/", "refs/remotes/"} {
			if short, ok := strings.CutPrefix(full, prefix); ok && short != "" {
				refs[short] = true
				if prefix == "refs/remotes/" {
					if _, branch, ok := strings.Cut(short, "/"); ok {
						refs[branch] = true
					}
				}
			}
		}
	}
	count := 0
	refsDir := filepath.Join(gitDir, "refs")
	_ = filepath.WalkDir(refsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || count > 5000 {
			return fs.SkipAll
		}
		if !d.IsDir() {
			count++
			if rel, rerr := filepath.Rel(gitDir, p); rerr == nil {
				addRef(filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if data, err := os.ReadFile(filepath.Join(gitDir, "packed-refs")); err == nil && len(data) < 4<<20 {
		for _, line := range strings.Split(string(data), "\n") {
			if _, ref, ok := strings.Cut(line, " "); ok && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "^") {
				addRef(ref)
			}
		}
	}
	return refs
}

// currentClause keeps only the clause the text ends in: "add X and change
// Y" checks Y against "change" only.
func currentClause(before string) string {
	if loc := nameCheckClause.FindAllStringIndex(before, -1); len(loc) > 0 {
		before = before[loc[len(loc)-1][1]:]
	}
	return before
}

// literalValue reports whether a backticked plain word (no _, no dot, no
// camel hump) is introduced as a literal value rather than a name.
func literalValue(text string, at int, c nameCheckCandidate) bool {
	if c.path || len(c.needles) != 1 || strings.Contains(c.name, "_") || strings.HasPrefix(c.name, "-") {
		return false
	}
	if strings.ToLower(c.name[1:]) != c.name[1:] {
		return false // camelCase is a name
	}
	return nameCheckValue.MatchString(currentClause(text[max(0, at-48):at]))
}

// nameCheckScan holds what one bounded workspace scan found.
type nameCheckScan struct {
	complete bool
	found    map[string]bool // needle -> found as a whole identifier/file name
	idents   map[string]struct{}
	files    map[string]struct{} // file base names
}

func scanWorkspaceForNames(ctx context.Context, root string, cands []nameCheckCandidate) nameCheckScan {
	scan := nameCheckScan{found: map[string]bool{}, idents: map[string]struct{}{}, files: map[string]struct{}{}}
	var needles []string
	for _, c := range cands {
		for _, n := range c.needles {
			if _, ok := scan.found[n]; !ok {
				scan.found[n] = false
				needles = append(needles, n)
			}
		}
	}
	deadline := time.Now().Add(nameCheckBudget)
	files := 0
	errStop := fs.SkipAll
	aborted := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			aborted = true
			return errStop
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && search.IsSkippedDirectory(name) {
				return fs.SkipDir
			}
			return nil
		}
		files++
		if len(scan.files) < nameCheckMaxFiles {
			scan.files[name] = struct{}{}
		}
		if files > nameCheckMaxFiles {
			aborted = true
			return errStop
		}
		for _, n := range needles {
			if !scan.found[n] && (name == n || strings.TrimSuffix(name, filepath.Ext(name)) == n) {
				scan.found[n] = true
			}
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > nameCheckMaxFileSize || !info.Mode().IsRegular() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		head := data
		if len(head) > 8000 {
			head = head[:8000]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil // binary
		}
		for _, n := range needles {
			if !scan.found[n] && containsIdentifier(data, n) {
				scan.found[n] = true
			}
		}
		if len(scan.idents) < nameCheckMaxIdents {
			for _, id := range nameCheckIdent.FindAll(data, -1) {
				scan.idents[string(id)] = struct{}{}
				if len(scan.idents) >= nameCheckMaxIdents {
					break
				}
			}
		}
		return nil
	})
	scan.complete = err == nil && !aborted
	return scan
}

// containsIdentifier reports whether needle occurs in data with no identifier
// character on either side (so `parse_header` does not match parse_headers).
func containsIdentifier(data []byte, needle string) bool {
	nb := []byte(needle)
	for off := 0; ; {
		i := bytes.Index(data[off:], nb)
		if i < 0 {
			return false
		}
		i += off
		end := i + len(nb)
		if (i == 0 || !identByte(data[i-1])) && (end >= len(data) || !identByte(data[end])) {
			return true
		}
		off = i + 1
	}
}

func identByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// missingNames returns the candidates the scan did not find, each with up to
// three similar existing identifiers.
func missingNames(cands []nameCheckCandidate, scan nameCheckScan) (missing []string, similar map[string][]string) {
	similar = map[string][]string{}
	for _, c := range cands {
		ok := true
		if c.path {
			ok = scan.found[c.needles[0]]
		} else {
			for _, n := range c.needles {
				if !scan.found[n] {
					ok = false
					break
				}
			}
		}
		if ok {
			continue
		}
		missing = append(missing, c.name)
		if c.path {
			similar[c.name] = similarIdentifiers(c.needles[0], scan.files)
			continue
		}
		last := c.needles[len(c.needles)-1]
		if scan.found[last] {
			continue
		}
		similar[c.name] = similarIdentifiers(last, scan.idents)
	}
	return missing, similar
}

func similarIdentifiers(name string, idents map[string]struct{}) []string {
	norm := func(s string) string { return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(s)) }
	target := norm(name)
	type scored struct {
		s string
		d int
	}
	var hits []scored
	for id := range idents {
		if id == name {
			continue
		}
		n := norm(id)
		if n == target {
			hits = append(hits, scored{id, 0})
			continue
		}
		limit := len(target) / 4
		if limit < 1 {
			limit = 1
		}
		if abs(len(n)-len(target)) > limit {
			continue
		}
		if d := boundedEditDistance(n, target, limit); d <= limit {
			hits = append(hits, scored{id, d})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].d != hits[j].d {
			return hits[i].d < hits[j].d
		}
		return hits[i].s < hits[j].s
	})
	var out []string
	for _, h := range hits {
		if len(out) == 3 {
			break
		}
		out = append(out, h.s)
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// boundedEditDistance is Levenshtein distance, giving up (limit+1) beyond limit.
func boundedEditDistance(a, b string, limit int) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if cur[j] < best {
				best = cur[j]
			}
		}
		if best > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// nameCheckNote is the request-tail text for the missing names.
func nameCheckNote(missing []string, similar map[string][]string) string {
	var b strings.Builder
	b.WriteString("[name check] Not found in the workspace by a quick scan:")
	for _, m := range missing {
		b.WriteString(" `" + m + "`")
		if s := similar[m]; len(s) > 0 {
			b.WriteString(" (similar existing: " + strings.Join(s, ", ") + ")")
		}
		b.WriteString(";")
	}
	b.WriteString(" This is a hint, not a rule: check with a search. If the request uses it as a new value or something " +
		"to create (a new setting value, a new branch or file, a standard-library name), just use or create it. " +
		"If the request treats it as an existing part of the code to change or use (a function, key, flag, file) and it " +
		"really is missing, do not invent it and do not apply that part to a similar-looking name: do the rest of the " +
		"request, then say in the final answer which name was not found (and the similar one, if any) and ask how to proceed.")
	return b.String()
}

// attachNameCheck runs the check for prompt and appends its note to the
// current user turn when a name is missing.
func (l *Loop) attachNameCheck(ctx context.Context, prompt string) {
	if !l.nameCheck || l.baseDir == "" || ctx.Err() != nil {
		return
	}
	cands := requestNameCandidates(prompt)
	// SuperCli's own tool names ("use web_fetch") are not workspace names.
	if l.registry != nil {
		kept := cands[:0]
		for _, c := range cands {
			if _, isTool := l.registry.Get(c.name); !isTool {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	// Existing git branches and tags are names too.
	if refs := gitRefNames(l.baseDir); len(refs) > 0 {
		kept := cands[:0]
		for _, c := range cands {
			if !refs[c.name] {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	if len(cands) == 0 {
		return
	}
	scan := scanWorkspaceForNames(ctx, l.baseDir, cands)
	if !scan.complete {
		return
	}
	missing, similar := missingNames(cands, scan)
	if len(missing) == 0 {
		return
	}
	l.appendCurrentUserContext(nameCheckNote(missing, similar))
}
