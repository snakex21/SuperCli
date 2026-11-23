package webgui

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/system/uilang"
)

// Info lines in the chat come from fixed English texts in the agent loop,
// the providers and this package. The browser shows a known text in the UI
// language (SYSTEM_TEXTS in 01-i18n.js); an unknown one stays English. This
// test finds every such text in the Go sources and checks that the GUI knows
// it, so a new notice cannot slip into the chat untranslated.

// guiNoticeAllowed lists texts that may stay as received, with the reason.
var guiNoticeAllowed = map[string]string{
	// Provider-specific one-off diagnostics that name a model setting; they
	// are rare and the model id and setting name are the useful part.
	`model "x" rejected image input; retrying without images`:                                 "provider diagnostic",
	`model "x" has been deprecated by the provider; now using "x"`:                             "provider diagnostic",
	`model "x" rejects reasoning effort together with tools on /chat/completions; tool turns continue without it (use provider type "responses" to keep reasoning with tools)`: "provider diagnostic",
}

type systemTextEntry struct {
	key string
	re  *regexp.Regexp
}

func loadSystemTexts(t *testing.T) []systemTextEntry {
	t.Helper()
	src, err := assetsFS.ReadFile("assets/js/01-i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`\{ key: "([^"]+)", re: /(.+)/, args:`)
	var out []systemTextEntry
	for _, m := range line.FindAllStringSubmatch(string(src), -1) {
		re, err := regexp.Compile(m[2])
		if err != nil {
			t.Fatalf("SYSTEM_TEXTS %s: regexp not portable to Go: %v", m[1], err)
		}
		out = append(out, systemTextEntry{key: m[1], re: re})
	}
	if len(out) < 10 {
		t.Fatalf("parsed only %d SYSTEM_TEXTS entries", len(out))
	}
	return out
}

// sampleFormat turns a Go format string into a concrete text: numbers for
// numeric verbs, value for %s/%v (quoted for %q).
func sampleFormat(format, value string) string {
	verb := regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)
	return verb.ReplaceAllStringFunc(format, func(v string) string {
		switch v[len(v)-1] {
		case '%':
			return "%"
		case 'd':
			return "12"
		case 'f', 'g':
			return "1.5"
		case 'q':
			return strconv.Quote(value)
		default:
			return value
		}
	})
}

// sampleValues stand in for %s/%v: a word, a retry progress ("3/5") and a
// duration, so patterns that expect digits there still match.
var sampleValues = []string{"x", "3/5", "1.5s"}

const sampleSep = "\x00"

// noticeSamples collects the texts given to NoticeEvent{Text: ...},
// Delta{Notice: ...} and notice/compact wire events in one package directory.
func noticeSamples(t *testing.T, dir string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed []*ast.File
	consts := map[string]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := stringLit(vs.Values[i]); ok {
							consts[id.Name] = s
						}
					}
				}
			}
		}
	}
	out := map[string]string{}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Local string variables: every literal or Sprintf assigned to them.
			locals := map[string][]ast.Expr{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range as.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && i < len(as.Rhs) {
						locals[id.Name] = append(locals[id.Name], as.Rhs[i])
					}
				}
				return true
			})
			var resolve func(e ast.Expr, depth int) []string
			resolve = func(e ast.Expr, depth int) []string {
				if depth > 3 {
					return nil
				}
				if s, ok := stringLit(e); ok {
					return []string{s}
				}
				switch v := e.(type) {
				case *ast.Ident:
					if s, ok := consts[v.Name]; ok {
						return []string{s}
					}
					var all []string
					for _, rhs := range locals[v.Name] {
						all = append(all, resolve(rhs, depth+1)...)
					}
					return all
				case *ast.CallExpr:
					sel, ok := v.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Sprintf" || len(v.Args) == 0 {
						return nil
					}
					var all []string
					for _, format := range resolve(v.Args[0], depth+1) {
						// Variants are joined; the check splits them again.
						var variants []string
						for _, value := range sampleValues {
							variants = append(variants, sampleFormat(format, value))
						}
						all = append(all, strings.Join(variants, sampleSep))
					}
					return all
				}
				return nil
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				typeName := ""
				switch tn := lit.Type.(type) {
				case *ast.Ident:
					typeName = tn.Name
				case *ast.SelectorExpr:
					typeName = tn.Sel.Name
				}
				field := map[string]string{"NoticeEvent": "Text", "Delta": "Notice", "wireEvent": "Text"}[typeName]
				if field == "" {
					return true
				}
				if typeName == "wireEvent" && !wireTypeIs(lit, "notice", "compact") {
					return true
				}
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == field {
						for _, s := range resolve(kv.Value, 0) {
							out[s] = fset.Position(kv.Pos()).String()
						}
					}
				}
				return true
			})
		}
	}
	return out
}

func wireTypeIs(lit *ast.CompositeLit, types ...string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Type" {
			s, _ := stringLit(kv.Value)
			for _, want := range types {
				if s == want {
					return true
				}
			}
		}
	}
	return false
}

func stringLit(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		// "a" + "b" constant concatenation.
		if v.Op != token.ADD {
			return "", false
		}
		l, ok1 := stringLit(v.X)
		r, ok2 := stringLit(v.Y)
		return l + r, ok1 && ok2
	}
	return "", false
}

func TestGUINoticesAreTranslated(t *testing.T) {
	entries := loadSystemTexts(t)
	samples := map[string]string{}
	for _, dir := range []string{"../agent", "../llm", "."} {
		for text, pos := range noticeSamples(t, dir) {
			samples[text] = pos
		}
	}
	// The scanner must keep seeing the texts from the reported screenshot.
	firsts := map[string]bool{}
	for text := range samples {
		firsts[strings.Split(text, sampleSep)[0]] = true
	}
	for _, want := range []string{
		"pruned 12 old tool result(s), reclaimed ~12 tokens (trigger=12/x)",
		"Preparing a shorter context for this model…",
	} {
		if !firsts[want] {
			t.Fatalf("scanner lost a known notice %q (found %d texts)", want, len(samples))
		}
	}
	var missing []string
	for text, pos := range samples {
		if !looksLikeProse(strings.Split(text, sampleSep)[0]) {
			continue
		}
		variants := strings.Split(text, sampleSep)
		if _, ok := guiNoticeAllowed[variants[0]]; ok {
			continue
		}
		matched := false
		for _, e := range entries {
			for _, v := range variants {
				matched = matched || e.re.MatchString(v)
			}
		}
		if !matched {
			missing = append(missing, pos+": "+strconv.Quote(variants[0]))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("chat notices without a GUI translation (add a SYSTEM_TEXTS entry in 01-i18n.js):\n%s", strings.Join(missing, "\n"))
	}
}

// looksLikeProse is false for texts that are only placeholders, file names,
// paths or commands: they are not sentences a translation could change.
func looksLikeProse(text string) bool {
	return len(englishWords(text)) >= 2
}

var (
	wordRE        = regexp.MustCompile(`[A-Za-z][A-Za-z']+`)
	placeholderRE = regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`)
	// Things that read the same in every language: `code`, quoted values,
	// file names and paths, identifiers, flags, key = value settings, URLs.
	technicalRE = regexp.MustCompile("`[^`]*`" + `|"[^"]*"|https?://\S+|\S*[/\\]\S*|\S+\.[A-Za-z0-9]{1,5}\b|\S*_\S*|--?[a-z][\w-]*|\b[\w.]+\s*=\s*\S+`)
)

// englishWords returns the ordinary words left after removing placeholders
// and technical tokens.
func englishWords(text string) []string {
	text = placeholderRE.ReplaceAllString(text, " ")
	text = technicalRE.ReplaceAllString(text, " ")
	var out []string
	for _, w := range wordRE.FindAllString(text, -1) {
		if len(w) >= 3 && w != "SuperCli" {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

// Catalog entries for chat notices and tool rows must really be translated:
// a value equal to English that still has English words is an oversight.
// Names, commands and placeholders alone never count.
func TestGUINoticeCatalogsAreNotEnglish(t *testing.T) {
	var english map[string]string
	data, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &english); err != nil {
		t.Fatal(err)
	}
	for _, e := range loadSystemTexts(t) {
		if english[e.key] == "" {
			t.Errorf("SYSTEM_TEXTS key %s is missing from en.json", e.key)
		}
	}
	prefixes := []string{"notice.", "goalTool.", "eventTag.", "toolHint."}
	for _, lang := range uilang.Languages() {
		if lang.Code == "en" {
			continue
		}
		data, err := os.ReadFile(filepath.Join("assets", "locales", lang.Code+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var catalog map[string]string
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		for key, source := range english {
			matches := false
			for _, p := range prefixes {
				matches = matches || strings.HasPrefix(key, p)
			}
			if !matches || catalog[key] != source {
				continue
			}
			if words := englishWords(source); len(words) >= 2 {
				t.Errorf("%s %s is still English: %q", lang.Code, key, source)
			}
		}
	}
}

func TestEnglishWordsIgnoresNamesAndCommands(t *testing.T) {
	for _, text := range []string{
		"zig-engine/src/native_world.zig",
		"go test ./internal/...",
		"task_parallel = false",
		"`supercli-web.exe --echo`",
		"{model}: {wait}",
		"README.md · build.bat",
	} {
		if w := englishWords(text); len(w) >= 2 {
			t.Errorf("%q counted as prose: %v", text, w)
		}
	}
	if w := englishWords("pruned 951 old tool result(s), reclaimed ~757123 tokens"); len(w) < 2 {
		t.Errorf("an English sentence was not seen as prose: %v", w)
	}
}
