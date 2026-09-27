package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func runWorkerHintProbe(t *testing.T, agent, prompt string, thin, advise, wantHint bool) {
	t.Helper()
	base := tools.NewRegistry()
	for _, name := range []string{"read_lines", "patch_file", "ctx_execute", "scratchpad"} {
		base.MustRegister(tools.Tool{Name: name, Description: name, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			t.Error("probe must not execute tools")
			return tools.Result{}, nil
		}})
	}
	provider := &stubProvider{name: "probe", scripts: [][]llm.Delta{
		{{Content: "findings", FinishReason: "stop"}},
		{{Content: "follow-up findings", FinishReason: "stop"}},
	}}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: base, ThinTools: thin, StableToolset: true})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	specs.MustRegister(SubAgent{Name: "custom", Description: "custom implementing worker", System: "Complete the task."})
	readonlySpec, _ := specs.Get("review")
	readonlySpec.Name = "custom-readonly"
	specs.MustRegister(readonlySpec)
	task, err := NewAgentTool(specs, parent, base, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"agent": agent, "prompt": prompt, "advise": advise})
	first, err := task.execute(context.Background(), raw)
	if err != nil || first.Err != nil {
		t.Fatalf("task failed: %v %+v", err, first)
	}
	worker, ok := task.Workers.Get("worker-1")
	if !ok {
		t.Fatal("missing worker")
	}
	followup := "Review another proposed fix for cache expiry. Do not edit files."
	if wantHint {
		followup = "Fix the next defect and run relevant tests."
	}
	next, _ := json.Marshal(map[string]any{"to": "worker-1", "message": followup})
	result, err := NewSendMessageTool(task.Workers).execute(context.Background(), next)
	if err != nil || result.Err != nil {
		t.Fatalf("continuation failed: %v %+v", err, result)
	}
	if len(provider.reqs) != 2 || worker.Runs != 2 {
		t.Fatal("unexpected model call or restarted worker")
	}
	for i, request := range provider.reqs {
		user := ""
		for _, message := range request {
			if message.Role == llm.RoleUser {
				user = message.Content
			}
		}
		rawPrompt := prompt
		if i == 1 {
			rawPrompt = followup
		}
		if strings.Contains(user, implementationVerificationInstruction) != wantHint {
			t.Fatalf("role=%s request=%d hint=%v, want=%v; added bytes=%d", agent, i, strings.Contains(user, implementationVerificationInstruction), wantHint, len(user)-len(rawPrompt))
		}
		if !wantHint && user != rawPrompt {
			t.Fatalf("read-only instruction rewritten: %q", user)
		}
	}
	if !wantHint {
		for _, name := range []string{"patch_file", "ctx_execute"} {
			if _, ok := worker.Loop.registry.Get(name); ok {
				t.Fatalf("read-only role received %s", name)
			}
		}
	}
	t.Logf("role=%s thin=%t advise=%t requests=2 automatic_hint_bytes_per_mutation=%d", agent, thin, advise, map[bool]int{false: 0, true: len(implementationVerificationInstruction) + 2}[wantHint])
}

func TestReadOnlyWorkersDoNotReceiveImplementationContract(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, role := range []string{"advisor", "explore", "plan", "review", "code", "general", "custom", "custom-readonly"} {
			t.Run(fmt.Sprintf("%s/thin=%v", role, thin), func(t *testing.T) {
				editable := role == "code" || role == "general" || role == "custom"
				prompt := "Review the proposed fix for cache expiry read-only. Identify minimal code changes and tests. Do not edit files or commit."
				if editable {
					prompt = "Fix the defect in production code and run relevant tests."
				}
				runWorkerHintProbe(t, role, prompt, thin, false, editable)
			})
		}
	}
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("advise/thin=%v", thin), func(t *testing.T) {
			runWorkerHintProbe(t, "code", "Suggest a fix and tests. Do not edit files.", thin, true, false)
		})
	}
}

func TestReadOnlyWorkerSavedPrompts(t *testing.T) {
	path := os.Getenv("SUPERCLI_READONLY_PROMPTS")
	if path == "" {
		t.Skip("optional saved delegation replay")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Seq           int
		Agent, Prompt string
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fmt.Sprint(fixture.Seq), func(t *testing.T) { runWorkerHintProbe(t, fixture.Agent, fixture.Prompt, false, false, false) })
	}
}
