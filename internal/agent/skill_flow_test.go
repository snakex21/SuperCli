package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type skillFlowProvider struct {
	calls  int
	second []llm.Message
}

func (p *skillFlowProvider) Name() string         { return "skill-flow" }
func (p *skillFlowProvider) SupportsVision() bool { return false }
func (p *skillFlowProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	ch := make(chan llm.Delta, 2)
	if p.calls == 1 {
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "skill-1", Name: "apply_skill", Arguments: `{"name":"alpha"}`}}
		ch <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		p.second = append([]llm.Message(nil), msgs...)
		ch <- llm.Delta{Content: "used guidance"}
		ch <- llm.Delta{FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

func TestApplySkillGuidanceReachesNextModelCall(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, "skills", "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Alpha\nfollow alpha guidance"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	applier := tools.NewSkillApplier(tools.NewDiscoverer(project, t.TempDir()))
	reg.MustRegister(applier.Spec())
	reg.MarkAlwaysOn("apply_skill")
	provider := &skillFlowProvider{}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	events, err := loop.Run(context.Background(), "use the alpha skill")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	for _, msg := range provider.second {
		if msg.Role == llm.RoleTool && msg.Name == "apply_skill" &&
			strings.Contains(msg.Content, "follow alpha guidance") {
			return
		}
	}
	t.Fatalf("applied skill guidance missing from second request: %+v", provider.second)
}

func TestAutomaticSkillGuidanceUsesOneToolCallOnBothRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			dir := filepath.Join(project, "skills", "officecli-docx")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: Edit Word documents\n---\nselected Word guidance"), 0600); err != nil {
				t.Fatal(err)
			}
			registry := tools.NewRegistry()
			applier := tools.NewSkillApplier(tools.NewDiscoverer(project, t.TempDir()))
			spec := applier.Spec()
			execute, calls := spec.Fn, 0
			spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				calls++
				return execute(ctx, args)
			}
			registry.MustRegister(spec)
			registry.MarkAlwaysOn("apply_skill")
			call := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "select-skill", Name: "apply_skill", Arguments: `{"query":"officecli docx","auto":true}`}}}
			if thin {
				call = []llm.Delta{{Content: "«apply_skill\nquery: officecli docx\nauto: true»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "auto-skill", scripts: [][]llm.Delta{call, {{Content: "Guidance received.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Edit the Word document.")) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if calls != 1 || provider.calls != 2 {
				t.Fatalf("tool calls=%d model calls=%d", calls, provider.calls)
			}
			for _, msg := range provider.reqs[1] {
				if msg.Role == llm.RoleTool && msg.Name == "apply_skill" && strings.Contains(msg.Content, "selected Word guidance") {
					return
				}
			}
			t.Fatal("selected guidance missing from next request")
		})
	}
}
