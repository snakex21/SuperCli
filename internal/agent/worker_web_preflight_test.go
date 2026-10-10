package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerPreflightOnlySkipsSelfContainedWebBriefing(t *testing.T) {
	for _, test := range []struct {
		name, prompt, expect string
		wantPreflight        bool
	}{
		{"public download", "Download the image from https://example.com/a.png.", "", false},
		{"public lookup", "Find a photo on the web.", "", false},
		{"project investigation", "Inspect the download handler in the project.", "", true},
		{"mixed web and project", "Download the image from https://example.com/a.png and run project tests.", "", true},
		{"expect requires project", "Download an image from the web.", "Changed project files and test results.", true},
		{"ambiguous continuation", "Continue the previous work.", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &capturingProvider{reply: "Finished."}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, nil, tools.NewRegistry(), provider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			collections := 0
			task.Preflight = func() string { collections++; return testRepoBlock }
			raw, _ := json.Marshal(map[string]any{"prompt": test.prompt, "expect": test.expect})
			result, err := task.execute(context.Background(), raw)
			if err != nil || result.Err != nil {
				t.Fatalf("worker: %v %v", err, result.Err)
			}
			requests := provider.requests()
			if len(requests) != 1 {
				t.Fatalf("model requests=%d, want 1", len(requests))
			}
			found := false
			for _, message := range requests[0] {
				if message.Role == llm.RoleUser && strings.Contains(message.Content, testRepoBlock) {
					found = true
				}
			}
			wantCollections := 0
			if test.wantPreflight {
				wantCollections = 1
			}
			if collections != wantCollections || found != test.wantPreflight {
				t.Fatalf("repo collections=%d block=%t; want %d/%t", collections, found, wantCollections, test.wantPreflight)
			}
		})
	}
}
