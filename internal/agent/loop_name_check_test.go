package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func candidateNames(prompt string) []string {
	var out []string
	for _, c := range requestNameCandidates(prompt) {
		out = append(out, c.name)
	}
	return out
}

func TestNameCheckCandidates(t *testing.T) {
	cases := []struct {
		prompt string
		want   []string
	}{
		{"In config/app.toml lower the rate limiter's `burst` to 20 and set `cache.ttl_seconds` to 600.",
			[]string{"burst", "cache.ttl_seconds", "config/app.toml"}},
		{"Change the default of --workers to 8 in app/cli.py; raise MAX_ATTEMPTS to 5.",
			[]string{"--workers", "app/cli.py", "MAX_ATTEMPTS"}},
		{"make formatIEC() use two decimals and call parse_header from src/net/http.ts",
			[]string{"formatIEC", "parse_header", "src/net/http.ts"}},
		// Names the user asks to create are not checked.
		{"Add a `--verbose` flag and create utils/strings.py with a helper slug_title.",
			nil},
		{"Rename `load_cfg` to `load_config` everywhere.", []string{"load_cfg"}},
		{"Dodaj nową funkcję `policz_sume` i zmień `MAX_ITEMS` na 10.", []string{"MAX_ITEMS"}},
		// URLs, plain words and pasted code are not names.
		{"See https://example.com/some_page.html and fix the bug please", nil},
		{"Fix this:\n```python\nnew_value = compute_total(x)\n```\n", nil},
		{"use `the whole command here` and `id`", nil},
		// Rename targets, literal values, globs, acronyms and new helpers.
		{"Rename the package-level function `money.ParseAmount` to `money.Parse` and update docs/API.md.",
			[]string{"money.ParseAmount", "docs/API.md"}},
		{"A section with no findings contains the single line `none`; names ending with `Admin` are exempt.", nil},
		{"Never edit any *_test.go file; region `UNKNOWN` counts, written like `-17.05`. Call the JSON API.", nil},
		{"Extract it into a single helper function named formatMoney(value).", nil},
		{"Write the answers to ANSWER.json:\n```\n{\"first_5xx\": 1}\n```\n- first_5xx: the first error.", nil},
		// audit-130 (ho3) false hints: language builtins, change targets,
		// output-format placeholders, git refs and list items under a
		// "split into new packages:" header.
		{"(a) `Stock.add()` and `Stock.reserve()` must raise `ValueError` when the quantity is zero or negative.",
			[]string{"Stock.add", "Stock.reserve"}},
		{"Throw a `TypeError` from parse(); use `JSON.parse` and `os.path.join`, return `None`.", nil},
		{"In `scripts/Deploy-App.ps1` change the region from `westeurope` to `polandcentral` (only the `$Region` assignment).",
			[]string{"scripts/Deploy-App.ps1", "westeurope", "Region"}},
		{"Set `log_level` to `verbose_debug`; zmień `MAX_ITEMS` na `MAX_ITEMS_V2`. Raise `pool_size` to `pool_max`.",
			[]string{"log_level", "MAX_ITEMS", "pool_size"}},
		{"Write the result to `callsites.txt`: one `path:line` per call, or `file:line:col`.", nil},
		{"Please integrate `feature/volume-discounts` with the current `main` on a NEW branch `release/1.4`: create it from `main` and merge.", nil},
		{"I'm on `feature/report`, which is behind `main`. Before I rebase it onto `main` myself, tell me.", nil},
		{"Split the package `gateway/internal/util` into three packages and delete it:\n- `internal/textutil` (package textutil): `Slugify`;\n- `internal/clock`: `FormatAgo`.",
			[]string{"gateway/internal/util", "Slugify", "FormatAgo"}},
		// Still checked: names the request treats as existing.
		{"Change the default of the `TILER_WORKER_COUNT` environment variable from 4 to 8.", []string{"TILER_WORKER_COUNT"}},
		{"Make `normalize_unit_name` in pantry/units.py also accept plurals; in config/flags.production.json turn `dark_mode` off.",
			[]string{"normalize_unit_name", "dark_mode", "pantry/units.py", "config/flags.production.json"}},
		{"Fix the bug in `parse_headers` on the `main` page of the docs.", []string{"parse_headers", "main"}},
	}
	for _, c := range cases {
		if got := candidateNames(c.prompt); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q:\n got %q\nwant %q", c.prompt, got, c.want)
		}
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestNameCheckFindsMissingAndSimilarNames(t *testing.T) {
	root := writeTree(t, map[string]string{
		"config/app.toml":            "[cache]\nttl_seconds = 300\n[limits]\nrate = 5\n",
		"app/http.py":                "def parse_headers(raw):\n    return raw\n",
		"settings.yml":               "debug: true\n",
		"node_modules/x/burst.js":    "const burst = 1\n",
		"app/cli.py":                 "parser.add_argument('--workers', default=4)\n",
		".github/workflows/ci.yml":   "on: push\n",
		"assets/logo.png":            "\x89PNG\x00\x00burst",
		"docs/notes/parse_header.md": "", // an empty file named like the function still counts as present
	})
	prompt := "Set `cache.ttl_seconds` to 600, lower `limits.burst` to 20, make parse_headers() strict, " +
		"bump --workers, fix parse_header, edit settings.yaml and .github/workflows/ci.yml and `retry_backof`."
	cands := requestNameCandidates(prompt)
	scan := scanWorkspaceForNames(context.Background(), root, cands)
	if !scan.complete {
		t.Fatal("scan incomplete")
	}
	missing, similar := missingNames(cands, scan)
	want := []string{"limits.burst", "retry_backof", "settings.yaml"}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("missing = %q, want %q", missing, want)
	}
	if got := similar["settings.yaml"]; !reflect.DeepEqual(got, []string{"settings.yml"}) {
		t.Fatalf("similar settings.yaml = %q", got)
	}
	note := nameCheckNote(missing, similar)
	for _, s := range []string{"`limits.burst`", "`settings.yaml` (similar existing: settings.yml)", "Not found in the workspace",
		"new value or something to create", "just use or create it", "do not invent it", "ask how to proceed"} {
		if !strings.Contains(note, s) {
			t.Fatalf("note lacks %q:\n%s", s, note)
		}
	}
}

func TestNameCheckIdentifierBoundary(t *testing.T) {
	if containsIdentifier([]byte("def parse_headers():"), "parse_header") {
		t.Fatal("parse_header matched inside parse_headers")
	}
	if !containsIdentifier([]byte("x = parse_header(y)"), "parse_header") {
		t.Fatal("parse_header not matched")
	}
	got := similarIdentifiers("parse_header", map[string]struct{}{"parse_headers": {}, "parse": {}, "ParseHeader": {}, "render_footer": {}})
	if !reflect.DeepEqual(got, []string{"ParseHeader", "parse_headers"}) {
		t.Fatalf("similar = %q", got)
	}
}

// The git branches and tags of the workspace repository count as existing
// names (ho3-git-* prompts name `feature/report`, `main`, ...).
func TestNameCheckGitRefsAreNames(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app.py":                             "x = 1\n",
		".git/HEAD":                          "ref: refs/heads/main\n",
		".git/refs/heads/main":               "0000000000000000000000000000000000000000\n",
		".git/refs/heads/feature/report":     "0000000000000000000000000000000000000000\n",
		".git/refs/remotes/origin/hotfix_42": "0000000000000000000000000000000000000000\n",
		".git/packed-refs":                   "# pack-refs with: peeled\n1111111111111111111111111111111111111111 refs/tags/v1.2.0\n",
	})
	refs := gitRefNames(root)
	for _, want := range []string{"main", "feature/report", "origin/hotfix_42", "hotfix_42", "v1.2.0"} {
		if !refs[want] {
			t.Fatalf("ref %q not found in %v", want, refs)
		}
	}
	prompt := "Show what `hotfix_42` changes compared with `stale_branch_x`."
	l := &Loop{nameCheck: true, baseDir: root}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: prompt}}
	l.attachNameCheck(context.Background(), prompt)
	got := l.Messages[0].TextOnly().Content
	_, note, _ := strings.Cut(got, "[name check]")
	if strings.Contains(note, "`hotfix_42`") || !strings.Contains(note, "`stale_branch_x`") {
		t.Fatalf("user turn = %q", got)
	}
}

// The ho3 prompts that produced false hints in audit-130, run against their
// fixtures: none of them may produce a note any more.
func TestNameCheckHo3FalseHintsGone(t *testing.T) {
	cases := []struct{ fixture, prompt string }{
		{"agent-ho3-multiturn-undo-rename", "(a) `Stock.add()` and `Stock.reserve()` must raise `ValueError` when the quantity is zero or negative.\n" +
			"(b) Rename the `StockItem` field `qty_on_hand` to `on_hand` everywhere in the package and the tests (including the JSON key in `inventory/api.py`)."},
		{"agent-ho3-win-crlf-bom", "1. In `config/app.ini` change the server port from 8080 to 8443.\n" +
			"2. In `scripts/Deploy-App.ps1` change the region from `westeurope` to `polandcentral` (only the `$Region` assignment)."},
		{"agent-ho3-large-repo-callsites", "Write the result to `callsites.txt` in the repo root: one `path:line` per call (POSIX slashes)."},
	}
	for _, c := range cases {
		root := filepath.Join("..", "..", "test", "go", "eval", "fixtures", c.fixture)
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("fixture %s: %v", c.fixture, err)
		}
		cands := requestNameCandidates(c.prompt)
		scan := scanWorkspaceForNames(context.Background(), root, cands)
		if !scan.complete {
			t.Fatalf("%s: scan incomplete", c.fixture)
		}
		if missing, _ := missingNames(cands, scan); len(missing) > 0 {
			t.Errorf("%s: false hint for %q", c.fixture, missing)
		}
	}
}

func TestNameCheckSilentWhenScanIncomplete(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < nameCheckMaxFiles+5; i++ {
		files[filepath.ToSlash(filepath.Join("d", strings.Repeat("x", 1+i%7), "f"+strconv.Itoa(i)+".txt"))] = "x"
	}
	root := writeTree(t, files)
	l := &Loop{nameCheck: true, baseDir: root}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "lower `burst` to 20"}}
	l.attachNameCheck(context.Background(), "lower `burst` to 20")
	if strings.Contains(l.Messages[0].TextOnly().Content, "[name check]") {
		t.Fatal("note attached although the scan could not cover the workspace")
	}
}

func TestNameCheckAttachesNoteToCurrentUserTurn(t *testing.T) {
	root := writeTree(t, map[string]string{"app/retry.py": "MAX_ATTEMPTS = 3\n"})
	prompt := "raise MAX_ATTEMPTS to 5 and lower `burst_limit` to 20"
	for _, on := range []bool{true, false} {
		l := &Loop{nameCheck: on, baseDir: root}
		l.Messages = []llm.Message{{Role: llm.RoleUser, Content: prompt}}
		l.attachNameCheck(context.Background(), prompt)
		got := l.Messages[0].TextOnly().Content
		if has := strings.Contains(got, "[name check]") && strings.Contains(got, "`burst_limit`") && !strings.Contains(got, "`MAX_ATTEMPTS`"); has != on {
			t.Fatalf("name_check=%v: user turn = %q", on, got)
		}
	}
	// Every name exists: nothing is added.
	l := &Loop{nameCheck: true, baseDir: root}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "raise MAX_ATTEMPTS to 5"}}
	l.attachNameCheck(context.Background(), "raise MAX_ATTEMPTS to 5")
	if got := l.Messages[0].TextOnly().Content; got != "raise MAX_ATTEMPTS to 5" {
		t.Fatalf("user turn changed: %q", got)
	}
}
