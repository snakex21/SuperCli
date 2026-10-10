package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerPreflightContextTakesPrecedenceAndPreservesBriefing(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, expect, block string
		wantCollections             int
	}{
		{"project", "Inspect the project command handler.", "Report verified paths.", testRepoBlock, 1},
		{"empty context source", "Inspect project files.", "", "", 1},
		{"public work", "Find a photo on the web.", "", testRepoBlock, 0},
		{"mixed project work", "Download an image from the web.", "Changed project files and test results.", testRepoBlock, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &capturingProvider{reply: "Finished."}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, nil, tools.NewRegistry(), provider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "current invocation")
			collections, legacyCollections := 0, 0
			task.Preflight = func() string { legacyCollections++; return "legacy repo block" }
			task.PreflightContext = func(got context.Context) string {
				collections++
				if got.Value(contextKey{}) != "current invocation" {
					t.Error("preflight lost the parent invocation context")
				}
				return tc.block
			}
			raw, _ := json.Marshal(map[string]any{"prompt": tc.prompt, "expect": tc.expect})
			result, err := task.execute(ctx, raw)
			if err != nil || result.Err != nil {
				t.Fatalf("worker: %v %v", err, result.Err)
			}
			if collections != tc.wantCollections || legacyCollections != 0 {
				t.Fatalf("collections=%d legacy=%d, want %d/0", collections, legacyCollections, tc.wantCollections)
			}
			requests := provider.requests()
			if len(requests) != 1 {
				t.Fatalf("model requests=%d, want one", len(requests))
			}
			found := false
			for _, message := range requests[0] {
				if strings.Contains(message.Content, "legacy repo block") {
					t.Fatal("legacy fallback ran despite the context source")
				}
				if tc.block != "" && strings.Contains(message.Content, tc.block) {
					found = true
					if message.Role == llm.RoleSystem {
						t.Fatal("preflight changed the system prefix")
					}
				}
			}
			if found != (tc.wantCollections > 0 && tc.block != "") {
				t.Fatalf("repo block present=%v", found)
			}
		})
	}
}

func TestCanceledWorkerPreflightDoesNotProbePrepareOrRegister(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "context source"
		if legacy {
			name = "legacy source"
		}
		t.Run(name, func(t *testing.T) {
			provider := &capturingProvider{reply: "Finished."}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			prepared, probes := 0, 0
			task, err := NewAgentTool(specs, nil, tools.NewRegistry(), provider, nil, func(cfg LoopConfig) (*Loop, error) {
				prepared++
				return NewLoop(cfg)
			})
			if err != nil {
				t.Fatal(err)
			}
			task.WorkerProvider = &capturingProvider{reply: "Worker reply."}
			task.WorkerPing = func(context.Context) error { probes++; return nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			if legacy {
				task.Preflight = func() string { close(started); <-ctx.Done(); return testRepoBlock }
			} else {
				task.PreflightContext = func(ctx context.Context) string { close(started); <-ctx.Done(); return testRepoBlock }
			}
			completed := make(chan tools.Result, 1)
			go func() {
				result, err := task.execute(ctx, json.RawMessage(`{"prompt":"Inspect project files."}`))
				if err != nil {
					result.Err = err
				}
				completed <- result
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("preflight did not start")
			}
			cancel()
			select {
			case result := <-completed:
				if !errors.Is(result.Err, context.Canceled) {
					t.Fatalf("result error=%v, want cancellation", result.Err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("worker preparation did not stop after preflight cancellation")
			}
			if probes != 0 || prepared != 0 || len(task.Workers.List()) != 0 || len(provider.requests()) != 0 {
				t.Fatalf("canceled preparation continued: probes=%d loops=%d workers=%d model=%d", probes, prepared, len(task.Workers.List()), len(provider.requests()))
			}
		})
	}
}
