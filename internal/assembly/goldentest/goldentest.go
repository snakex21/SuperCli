// Package goldentest renders the runtime composition of a surface (TUI,
// batch, web) into stable text so golden tests can prove that refactoring the
// composition roots leaves every surface byte-identical: registered tools and
// their contracts, the always-on set, the assembled system prompt, a projection
// of agent.LoopConfig, the approval gate and the exact provider requests of a
// short scripted conversation.
//
// It is imported only by tests. Run `go test ... -update` to rewrite goldens.
package goldentest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

var update = flag.Bool("update", false, "rewrite runtime composition golden files")

// Updating reports whether -update was passed.
func Updating() bool { return *update }

// Dir is the per-OS golden directory under testdata. Platform hints, shell
// guidance and tool descriptions legitimately differ between operating
// systems, so each OS keeps its own snapshots.
func Dir(base string) string {
	return filepath.Join(base, "runtime_golden", runtime.GOOS)
}

// Check compares got with the golden file, or rewrites it with -update. When
// no golden exists for this OS the test is skipped instead of failing, so a
// snapshot captured on one OS never blocks another.
func Check(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("no golden %s for %s (run with -update to capture)", filepath.Base(path), runtime.GOOS)
	}
	if err != nil {
		t.Fatal(err)
	}
	// core.autocrlf checkouts may turn LF into CRLF; goldens are LF.
	want = []byte(strings.ReplaceAll(string(want), "\r\n", "\n"))
	if string(want) == got {
		return
	}
	wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			t.Fatalf("golden %s differs at line %d:\nwant: %q\ngot:  %q\n(run with -update only if the change is intended)", filepath.Base(path), i+1, clip(w), clip(g))
		}
	}
	t.Fatalf("golden %s differs", filepath.Base(path))
}

func clip(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// Normalizer replaces volatile values (temp paths, dates, ids) with stable
// placeholders.
type Normalizer struct {
	pairs [][2]string
}

// Path registers a directory and its common spellings (native, slash and
// JSON-escaped) for replacement by placeholder.
func (n *Normalizer) Path(dir, placeholder string) {
	if dir == "" {
		return
	}
	variants := []string{dir, filepath.ToSlash(dir), strings.ReplaceAll(dir, `\`, `\\`)}
	if abs, err := filepath.EvalSymlinks(dir); err == nil && abs != dir {
		variants = append(variants, abs, filepath.ToSlash(abs), strings.ReplaceAll(abs, `\`, `\\`))
	}
	for _, v := range variants {
		n.pairs = append(n.pairs, [2]string{v, placeholder})
	}
	// Longest first so a nested temp dir never leaves a partial prefix.
	sort.SliceStable(n.pairs, func(i, j int) bool { return len(n.pairs[i][0]) > len(n.pairs[j][0]) })
}

// Literal registers one exact replacement.
func (n *Normalizer) Literal(old, placeholder string) {
	if old != "" {
		n.pairs = append(n.pairs, [2]string{old, placeholder})
		sort.SliceStable(n.pairs, func(i, j int) bool { return len(n.pairs[i][0]) > len(n.pairs[j][0]) })
	}
}

var volatile = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\b(sess|batch|web|chat)-\d{9,}\b`), "$1-<ID>"},
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?`), "<DATE>"},
	{regexp.MustCompile(`\b\d{1,2}:\d{2}(:\d{2})?\b`), "<TIME>"},
	{regexp.MustCompile(`\(\d+(ns|µs|ms|s|m|h|d|w|mo|y)\)`), "(<AGE>)"},
	{regexp.MustCompile(`\bIt is \d{4}\b`), "It is <YEAR>"},
	{regexp.MustCompile(`\b(Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday)\b`), "<WEEKDAY>"},
	{regexp.MustCompile(`\b(January|February|March|April|May|June|July|August|September|October|November|December)\b`), "<MONTH>"},
	{regexp.MustCompile(`UTC[+-]\d{2}:?\d{2}|\b[A-Z]{3,4}\b \(UTC[^)]*\)`), "<TZ>"},
}

// Apply normalises s.
func (n *Normalizer) Apply(s string) string {
	for _, p := range n.pairs {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	for _, v := range volatile {
		s = v.re.ReplaceAllString(s, v.repl)
	}
	return s
}

func short(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// FuncName names a function value for wrapper detection: the outermost Fn of
// a tool is the closure of the last wrapper applied (checkpoint, code-intel
// invalidation) or the tool's own implementation.
func FuncName(fn any) string {
	if fn == nil {
		return "nil"
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return "nil"
	}
	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return "?"
	}
	return f.Name()
}

// Wrappers returns tool name -> wrapper label recorded by a test-supplied
// checkpoint wrapper (see RecordingWrapper).
type WrapRecorder struct {
	mu    sync.Mutex
	order []string
}

// Wrap is a pass-through checkpoint wrapper that records which tools it saw.
func (r *WrapRecorder) Wrap(spec tools.Tool) tools.Tool {
	r.mu.Lock()
	r.order = append(r.order, spec.Name)
	r.mu.Unlock()
	return spec
}

// String lists the wrapped tool names in wrapping order.
func (r *WrapRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.order, ",")
}

// ToolOrder returns every registered tool name in registration order. It
// temporarily activates everything, then restores the original active set, so
// only use it on a registry that is not about to be driven.
func ToolOrder(reg *tools.Registry) []string {
	active := reg.ActiveNames()
	reg.Activate(reg.Names()...)
	order := reg.VisibleNames()
	reg.ResetVisibility()
	reg.Activate(active...)
	return order
}

// Registry renders a registry: one line per tool in registration order with
// visibility, contract flags, description/schema hashes and the outermost Fn.
func Registry(reg *tools.Registry, n *Normalizer) string {
	if reg == nil {
		return "<nil registry>\n"
	}
	visible := map[string]bool{}
	for _, name := range reg.VisibleNames() {
		visible[name] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "visible: %s\n", strings.Join(reg.VisibleNames(), ","))
	for _, name := range ToolOrder(reg) {
		t, _ := reg.Get(name)
		flags := []string{}
		if visible[name] {
			flags = append(flags, "on")
		}
		if t.ReadOnly {
			flags = append(flags, "ro")
		}
		if t.Approval != nil {
			flags = append(flags, "approval")
		}
		if t.ApprovalArg != "" {
			flags = append(flags, "arg="+t.ApprovalArg)
		}
		fmt.Fprintf(&b, "%s [%s] desc=%s schema=%s fn=%s\n", name, strings.Join(flags, " "),
			short(n.Apply(t.Description)), short(n.Apply(t.Schema)), FuncName(t.Fn))
	}
	return b.String()
}

// LoopConfig projects agent.LoopConfig: scalar values verbatim, hooks and
// references as set/nil, slices as their length, plus the provider name and
// registry size.
func LoopConfig(cfg agent.LoopConfig, n *Normalizer) string {
	var b strings.Builder
	v := reflect.ValueOf(cfg)
	ty := v.Type()
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		var s string
		switch fv.Kind() {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			s = fmt.Sprint(fv.Interface())
		case reflect.String:
			val := fv.String()
			if len(val) > 120 {
				s = fmt.Sprintf("len=%d sha=%s", len(val), short(n.Apply(val)))
			} else {
				s = fmt.Sprintf("%q", n.Apply(val))
			}
		case reflect.Slice, reflect.Map:
			if fv.IsNil() {
				s = "nil"
			} else {
				s = fmt.Sprintf("len=%d", fv.Len())
			}
		case reflect.Func, reflect.Pointer, reflect.Interface, reflect.Chan:
			if fv.IsNil() {
				s = "nil"
			} else {
				s = "set"
			}
		case reflect.Struct:
			s = fmt.Sprintf("%+v", fv.Interface())
		default:
			s = fv.Kind().String()
		}
		fmt.Fprintf(&b, "%s: %s\n", f.Name, s)
	}
	if cfg.Provider != nil {
		fmt.Fprintf(&b, "provider.name: %s\n", llm.ProviderModelName(cfg.Provider))
	}
	if cfg.Registry != nil {
		fmt.Fprintf(&b, "registry.len: %d\n", cfg.Registry.Len())
	}
	if cfg.MaxSteps != 0 {
		fmt.Fprintf(&b, "max_steps.default: %v\n", cfg.MaxSteps == agent.DefaultMaxSteps)
	}
	if cfg.LiveContextBlocks != nil {
		blocks, err := cfg.LiveContextBlocks(context.Background())
		fmt.Fprintf(&b, "live_context.err: %v\n", err)
		for _, blk := range blocks {
			fmt.Fprintf(&b, "live_context[%s]: %q\n", blk.Source, n.Apply(blk.Text))
		}
	}
	if cfg.UserInstructionsForRun != nil {
		text, err := cfg.UserInstructionsForRun(context.Background())
		fmt.Fprintf(&b, "user_instructions_for_run: %q err=%v\n", n.Apply(text), err)
	}
	return b.String()
}

// ApprovalGate projects the gate configuration.
func ApprovalGate(g *tools.ApprovalGate, n *Normalizer) string {
	if g == nil {
		return "<nil gate>\n"
	}
	lang := "<nil>"
	if g.Language != nil {
		lang = g.Language()
	}
	return fmt.Sprintf("fallback: %v\nauto_approve: %q\nallow_all_non_interactive: %v\nnon_interactive_reason: %q\ngrant_ttl: %s\ntimeout: %s\naudit_path: %s\nlanguage: %s\nnow: %v\n",
		g.Fallback != nil, g.AutoApprove, g.AllowAllNonInteractive, g.NonInteractiveReason,
		g.GrantTTL, g.Timeout, n.Apply(g.AuditPath), lang, g.Now != nil)
}

// ScriptedProvider is a deterministic provider for golden transcripts. The
// first request of a user turn that names a tool (prompt "GOLDEN_CALL <tool> <json>")
// answers with that tool call; a request whose last message is a tool result
// answers with plain text. Every other request (helper inferences such as
// reflection or summaries) answers "noop". All requests are recorded.
type ScriptedProvider struct {
	ModelName string
	mu        sync.Mutex
	requests  []recorded
	calls     int
}

type recorded struct {
	msgs  []llm.Message
	tools []llm.ToolDef
}

// CallMarker in a user prompt makes ScriptedProvider answer with a tool call.
const CallMarker = "GOLDEN_CALL "

// Name implements llm.Provider.
func (p *ScriptedProvider) Name() string { return p.ModelName }

// Complete implements llm.Provider.
func (p *ScriptedProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, recorded{msgs: append([]llm.Message(nil), msgs...), tools: append([]llm.ToolDef(nil), defs...)})
	p.calls++
	id := p.calls
	p.mu.Unlock()
	out := make(chan llm.Delta, 4)
	// The runtime tail (workspace, date, reminders) rides after the newest
	// conversational message, so look at the last non-system message.
	last := llm.Message{}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.RoleSystem {
			last = msgs[i]
			break
		}
	}
	go func() {
		defer close(out)
		usage := &llm.Usage{Input: 100, Output: 10, Total: 110}
		if last.Role == llm.RoleUser {
			text := messageText(last)
			if i := strings.Index(text, CallMarker); i >= 0 {
				rest := strings.TrimSpace(text[i+len(CallMarker):])
				name, args, _ := strings.Cut(rest, " ")
				if j := strings.Index(args, "}"); j >= 0 {
					args = args[:j+1]
				}
				out <- llm.Delta{Role: llm.RoleAssistant}
				out <- llm.Delta{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("call_%d", id), Name: name, Arguments: args}}
				out <- llm.Delta{FinishReason: "tool_calls", Usage: usage}
				return
			}
			out <- llm.Delta{Role: llm.RoleAssistant, Content: "plain answer"}
			out <- llm.Delta{FinishReason: "stop", Usage: usage}
			return
		}
		if last.Role == llm.RoleTool {
			out <- llm.Delta{Role: llm.RoleAssistant, Content: "done after tool"}
			out <- llm.Delta{FinishReason: "stop", Usage: usage}
			return
		}
		out <- llm.Delta{Role: llm.RoleAssistant, Content: "noop"}
		out <- llm.Delta{FinishReason: "stop", Usage: usage}
	}()
	return out, nil
}

func messageText(m llm.Message) string {
	if m.Content != "" {
		return m.Content
	}
	var parts []string
	for _, p := range m.Parts {
		if p.Type == llm.PartTypeText {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// originVolatile strips byte counts and content digests from context-origin
// metadata: they hash the dated runtime tail and temp paths.
var originVolatile = regexp.MustCompile(`,?"(bytes|digest)":(\[[^\]]*\]|\d+)`)

// Transcript renders every recorded request.
func (p *ScriptedProvider) Transcript(n *Normalizer) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	for i, r := range p.requests {
		fmt.Fprintf(&b, "=== request %d ===\n", i+1)
		names := make([]string, 0, len(r.tools))
		for _, d := range r.tools {
			names = append(names, fmt.Sprintf("%s(desc=%s schema=%s)", d.Name, short(n.Apply(d.Description)), short(n.Apply(d.Schema))))
		}
		fmt.Fprintf(&b, "tools: %s\n", strings.Join(names, " "))
		for j, m := range r.msgs {
			fmt.Fprintf(&b, "--- msg %d role=%s", j, m.Role)
			if m.Name != "" {
				fmt.Fprintf(&b, " name=%s", m.Name)
			}
			if m.ToolCallID != "" {
				fmt.Fprintf(&b, " tool_call_id=%s", m.ToolCallID)
			}
			if m.Origin != nil {
				raw, _ := json.Marshal(m.Origin)
				fmt.Fprintf(&b, " origin=%s", originVolatile.ReplaceAllString(n.Apply(string(raw)), ""))
			}
			b.WriteString("\n")
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "tool_call %s %s %s\n", tc.ID, tc.Name, n.Apply(tc.Arguments))
			}
			if m.Content != "" {
				b.WriteString(n.Apply(m.Content))
				b.WriteString("\n")
			}
			for k, part := range m.Parts {
				fmt.Fprintf(&b, "[part %d %s source=%s]\n", k, part.Type, part.Source)
				if part.Text != "" {
					b.WriteString(n.Apply(part.Text))
					b.WriteString("\n")
				}
			}
		}
	}
	return b.String()
}

// Drive runs prompts sequentially through loop, draining events, and returns
// a compact event log (event kinds and tool names).
func Drive(t *testing.T, loop *agent.Loop, prompts ...string) string {
	t.Helper()
	var b strings.Builder
	for _, prompt := range prompts {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		ch, err := loop.Run(ctx, prompt)
		if err != nil {
			cancel()
			t.Fatalf("run %q: %v", prompt, err)
		}
		for ev := range ch {
			switch e := ev.(type) {
			case agent.ToolCallEvent:
				fmt.Fprintf(&b, "tool_call %s\n", e.Name)
			case agent.ErrorEvent:
				fmt.Fprintf(&b, "error %v\n", e.Err)
			case agent.DoneEvent:
				fmt.Fprintf(&b, "done steps=%d\n", e.Steps)
			}
		}
		// The web surface finalises host-power ownership after each run
		// (external mode); elsewhere this is a no-op.
		loop.FinalizeHostPower(nil)
		cancel()
	}
	return b.String()
}

// Section formats one titled block of a golden file.
func Section(title, body string) string {
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "##### " + title + "\n" + body
}

// Profile is one execution profile of the golden matrix.
type Profile struct {
	Name string
	// Tier is the model tier forced through [[model_tiers]]: "big" (full
	// prompt and native schemas) or "small" (compact prompt, thin tools).
	Tier string
	// Orchestrator is the tri-state delegation policy: nil adaptive, true
	// hard orchestrator, false direct (delegation disabled).
	Orchestrator *bool
}

func boolp(v bool) *bool { return &v }

// Profiles is the shared matrix: model tier x delegation policy.
var Profiles = []Profile{
	{Name: "full-adaptive", Tier: "big"},
	{Name: "full-orchestrator", Tier: "big", Orchestrator: boolp(true)},
	{Name: "full-direct", Tier: "big", Orchestrator: boolp(false)},
	{Name: "small-adaptive", Tier: "small"},
	{Name: "small-orchestrator", Tier: "small", Orchestrator: boolp(true)},
	{Name: "small-direct", Tier: "small", Orchestrator: boolp(false)},
}

// Model and BaseURL are the provider identity used by every golden surface.
// The host is non-local so the tier alone decides thin vs full tools.
const (
	Model   = "golden-model"
	BaseURL = "https://golden.invalid/v1"
)

// Fixture writes a workspace and data directory for p: config.toml with the
// tier rule, delegation policy and approvals; AGENTS.md project instructions;
// a model profile; and an enabled user-instructions preset.
func Fixture(t *testing.T, p Profile) (home, dataDir string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "home")
	dataDir = filepath.Join(root, "data")
	for _, d := range []string{home, dataDir, filepath.Join(dataDir, "profiles")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var cfg strings.Builder
	cfg.WriteString("language = \"pl\"\n")
	if p.Orchestrator != nil {
		fmt.Fprintf(&cfg, "orchestrator = %v\n", *p.Orchestrator)
	}
	fmt.Fprintf(&cfg, "\n[[model_tiers]]\npattern = \"golden-*\"\ntier = %q\n", p.Tier)
	cfg.WriteString("\n[approvals]\nauto_approve = [\"thunderbird_mail:count\"]\nsession_grant_minutes = 45\n")
	files := map[string]string{
		filepath.Join(dataDir, "config.toml"):            cfg.String(),
		filepath.Join(home, "AGENTS.md"):                 "# Golden project\nAlways answer briefly.\n",
		filepath.Join(home, "notes.txt"):                 "golden workspace file\n",
		filepath.Join(dataDir, "profiles", "default.md"): "Golden model profile: prefer small steps.\n",
		filepath.Join(dataDir, "user-instructions.json"): `{"version":1,"enabled":true,"active_id":"g","presets":[{"id":"g","name":"Golden","content":"Golden user preset: reply in English."}]}`,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Distinct modification times keep "recently modified" listings in the
	// repository preflight block in a stable order.
	base := time.Now().Add(-48 * time.Hour)
	for i, name := range []string{"AGENTS.md", "notes.txt"} {
		stamp := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(filepath.Join(home, name), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	return home, dataDir
}

// CleanEnv clears environment overrides that would leak the developer's
// configuration into a golden run.
func CleanEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "SUPERCLI_") || strings.HasPrefix(key, "BRAVE_") || strings.HasPrefix(key, "TAVILY_") {
			t.Setenv(key, "")
			_ = os.Unsetenv(key)
		}
	}
}

// NewNormalizer returns a normaliser for one fixture.
func NewNormalizer(home, dataDir string) *Normalizer {
	n := &Normalizer{}
	n.Path(home, "<HOME>")
	n.Path(dataDir, "<DATA>")
	n.Path(filepath.Dir(home), "<ROOT>")
	if dl := os.Getenv("USERPROFILE"); dl != "" {
		n.Path(dl, "<USER>")
	}
	if dl := os.Getenv("HOME"); dl != "" {
		n.Path(dl, "<USER>")
	}
	n.Path(os.TempDir(), "<TMP>")
	return n
}
