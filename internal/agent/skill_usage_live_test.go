package agent

// Opt-in diagnostic: the model sees the ordinary core and a project-specific
// review skill. No shell, edit, mail or other mutating tool is registered.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/tools"
)

func TestProjectSkillUseLive(t *testing.T) {
	endpoint, model := os.Getenv("SUPERCLI_SKILL_TEST_URL"), os.Getenv("SUPERCLI_SKILL_TEST_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set SUPERCLI_SKILL_TEST_URL/MODEL for the isolated local skill diagnostic")
	}
	if !llm.IsLocalBaseURL(endpoint) {
		t.Fatal("skill diagnostic requires a local endpoint")
	}
	thin := os.Getenv("SUPERCLI_SKILL_TEST_THIN") == "true"
	temperature := 0.0
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: endpoint, Model: model, MaxTokens: 1024, Timeout: 90 * time.Second, Sampling: llm.Sampling{Temperature: &temperature}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "project-review")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: project-review\ndescription: Project review procedure for Go arithmetic code correctness\n---\nProject review procedure: identify division by zero and document input contracts. Apply Go semantics: signed overflow is not automatically a runtime panic. Report findings as risk, trigger, fix. Keep the answer concise."
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	applier := tools.NewSkillApplier(&tools.Discoverer{Sources: []tools.Source{{Dir: filepath.Join(root, "skills"), Priority: 100}}})
	registry.MustRegister(applier.Spec())
	registry.MarkAlwaysOn("apply_skill")
	registry.MustRegister(tools.NewListDir(root).Spec())
	registry.MarkAlwaysOn("list_dir")
	registry.MustRegister(NewInvokeTool(registry).Spec())
	registry.MarkAlwaysOn("invoke_tool")
	registry.MustRegister(tools.NewToolSearcher(registry, nil).Spec())
	registry.MarkAlwaysOn("tool_search")
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: llmprompt.Core, BaseDir: root, ThinTools: thin, StableToolset: true, CatalogHoist: thin, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	events, err := loop.Run(ctx, "Review this Go function using our project's review procedure: func divide(a, b int) int { return a / b }. Do not modify files.")
	if err != nil {
		t.Fatal(err)
	}
	var sequence, trace []string
	var answer strings.Builder
	for event := range events {
		switch event := event.(type) {
		case ToolCallEvent:
			sequence = append(sequence, event.Name)
			trace = append(trace, event.Name+" "+event.Args)
		case MessageEvent:
			answer.WriteString(event.Text)
		case ErrorEvent:
			t.Errorf("model run: %v", event.Err)
		}
	}
	report, _ := json.Marshal(map[string]any{"model": model, "thin": thin, "tools": sequence, "trace": trace, "applied": applier.Applied(), "answer": answer.String()})
	t.Log(string(report))
	if len(applier.Applied()) != 1 || applier.Applied()[0] != "project-review" {
		t.Error("model did not retrieve the requested project review procedure")
	}
}
