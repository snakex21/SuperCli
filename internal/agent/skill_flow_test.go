package agent

import (
	"context"
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
