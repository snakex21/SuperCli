package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestInvokeTextPreservesPunctuation(t *testing.T) {
	for _, tc := range []struct {
		text string
		want map[string]string
	}{
		{"include: *.{go,ts}\nquery: cache{1,3};expired\nmax: 3", map[string]string{"include": "*.{go,ts}", "query": "cache{1,3};expired", "max": "3"}},
		{"include: *.{go,ts}, query: cache{1,3};expired; max: 3", map[string]string{"include": "*.{go,ts}", "query": "cache{1,3};expired", "max": "3"}},
		{`path: C:\repo\a,b;c.go`, map[string]string{"path": `C:\repo\a,b;c.go`}},
		{`query: "alpha, limit: 9; path: other", limit: 3`, map[string]string{"query": `"alpha, limit: 9; path: other"`, "limit": "3"}},
		{`query: "escaped \"quote\", limit: 9", limit: 3`, map[string]string{"query": `"escaped \"quote\", limit: 9"`, "limit": "3"}},
		{"query: [a,b:c],limit:3", map[string]string{"query": "[a,b:c]", "limit": "3"}},
		{`query: [^"], limit: 3`, map[string]string{"query": `[^"]`, "limit": "3"}},
		{`query: \[literal, limit: 3`, map[string]string{"query": `\[literal`, "limit": "3"}},
		{"query: alpha,limit:3", map[string]string{"query": "alpha", "limit": "3"}},
		{"query: alpha\r\nlimit: 3", map[string]string{"query": "alpha", "limit": "3"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			raw, _ := json.Marshal(tc.text)
			got, err := decodeInvokeArgs(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got=%s want=%v", got, tc.want)
			}
			for k, want := range tc.want {
				var value string
				if err := json.Unmarshal(got[k], &value); err != nil || value != want {
					t.Fatalf("%s=%q want %q; error=%v", k, value, want, err)
				}
			}
		})
	}
}

func TestInvokeTextRejectsDuplicateAndMalformedFields(t *testing.T) {
	for _, text := range []string{"path: first\npath: second", "path: first, path: first", "path: first; path: second", "query: okay\nmalformed"} {
		raw, _ := json.Marshal(text)
		if got, err := decodeInvokeArgs(raw); err == nil {
			t.Fatalf("accepted %q: %s", text, got)
		}
	}
}

func TestInvokeTextPunctuationReachesBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "thin"}[thin], func(t *testing.T) {
			reg := tools.NewRegistry()
			calls := 0
			reg.MustRegister(tools.Tool{Name: "lookup", Description: "fixture", ReadOnly: true, Schema: `{"type":"object","properties":{"include":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"}}}`, Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				calls++
				var got struct {
					Include string
					Query   string
					Limit   int
				}
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				if got.Include != "*.{go,ts}" || got.Query != "cache{1,3};expired" || got.Limit != 3 {
					t.Fatalf("altered arguments: %s", raw)
				}
				return tools.Result{Text: "cache.go:7: cached value expired"}, nil
			}})
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn(invokeToolName)
			raw, _ := json.Marshal(map[string]string{"tool": "lookup", "args": "include: *.{go,ts}, query: cache{1,3};expired, limit: 3"})
			script := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "lookup-1", Name: invokeToolName, Arguments: string(raw)}}}
			if thin {
				script = []llm.Delta{{Content: "«invoke_tool\ntool: lookup\nargs: include: *.{go,ts}, query: cache{1,3};expired, limit: 3»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "invoke-punctuation", scripts: [][]llm.Delta{script, {{Content: "Found the expiry check.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Find the expiry check.")) {
				if problem, ok := event.(ErrorEvent); ok {
					t.Fatal(problem.Err)
				}
			}
			if calls != 1 || provider.calls != 2 {
				t.Fatalf("target calls=%d provider calls=%d", calls, provider.calls)
			}
			found := false
			for _, message := range provider.reqs[1] {
				if message.Role == llm.RoleTool && strings.Contains(message.Content, "cached value expired") {
					found = true
				}
			}
			if !found {
				t.Fatal("result never reached the next model request")
			}
		})
	}
}

func TestInvokeTextStillEnforcesTargetRules(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "guarded", Description: "fixture", Schema: `{"path":{"type":"string"}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		t.Fatal("resolver must never execute tools")
		return tools.Result{}, nil
	}})
	envelope := func(args string) llm.ToolCall {
		raw, _ := json.Marshal(map[string]string{"tool": "guarded", "args": args})
		return llm.ToolCall{Name: invokeToolName, Arguments: string(raw)}
	}
	if _, err := resolveInvokeToolCall(reg, envelope("path: a,b;c.go")); err == nil {
		t.Fatal("inactive mutation accepted")
	}
	reg.Activate("guarded")
	for _, args := range []string{"path: a,b;c.go, unknown: 1", "path: first, path: second"} {
		if _, err := resolveInvokeToolCall(reg, envelope(args)); err == nil {
			t.Fatalf("accepted invalid target args %q", args)
		}
	}
	resolved, err := resolveInvokeToolCall(reg, envelope("path: a,b;c.go"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Arguments != `{"path":"a,b;c.go"}` {
		t.Fatalf("altered target args: %s", resolved.Arguments)
	}
}
