package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestSourceSearchAndReadWorkThroughBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			for _, dir := range []string{"src/catalog", ".zig-cache/h"} {
				if err := os.MkdirAll(filepath.Join(home, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			const source = "pub const DetectedSystem = enum { unknown, windows_modern };\n"
			if err := os.WriteFile(filepath.Join(home, "src/catalog/detected_system.zig"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".zig-cache/h/old.txt"), []byte(strings.Repeat("DetectedSystem stale cache\n", 100)), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewSearchCode(home).Spec())
			reg.MustRegister(tools.NewReadLines(home).Spec())
			reg.MarkAlwaysOn("search_code")
			reg.MarkAlwaysOn("read_lines")
			makeCall := func(id, tool string, args map[string]any, sentinel string) []llm.Delta {
				if thin {
					return []llm.Delta{{Content: sentinel, FinishReason: "stop"}}
				}
				raw, _ := json.Marshal(args)
				return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: tool, Arguments: string(raw)}}}
			}
			provider := &stubProvider{name: "source-search", scripts: [][]llm.Delta{
				makeCall("search", "search_code", map[string]any{"query": "DetectedSystem", "max": 1, "include": "*.zig"}, "«search_code\nquery: DetectedSystem\nmax: 1\ninclude: *.zig»"),
				makeCall("read", "read_lines", map[string]any{"file": "src/catalog/detected_system.zig", "from": 1, "to": 2}, "«read_lines\nfile: src/catalog/detected_system.zig\nfrom: 1\nto: 2»"),
				{{Content: "Found the definition.", FinishReason: "stop"}},
			}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 4})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Find and read the DetectedSystem definition.")) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if provider.calls != 3 {
				t.Fatalf("calls=%d", provider.calls)
			}
			var search, read string
			for _, message := range provider.reqs[2] {
				if message.Role != llm.RoleTool {
					continue
				}
				if message.Name == "search_code" {
					search = message.Content
				}
				if message.Name == "read_lines" {
					read = message.Content
				}
			}
			if !strings.HasPrefix(search, "src/catalog/detected_system.zig:1:"+strings.TrimSpace(source)+"\n[search limit reached: 1") || !strings.Contains(read, source) {
				t.Fatalf("search=%q read=%q", search, read)
			}
		})
	}
}

func TestFileDiscoveryWorksThroughBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "empty.go"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewSearchCode(home).Spec())
			reg.MarkAlwaysOn("search_code")
			first := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "paths", Name: "search_code", Arguments: "{\"include\":\"*.go\"}"}}}
			if thin {
				first = []llm.Delta{{Content: "«search_code\ninclude: *.go»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "file-discovery", scripts: [][]llm.Delta{
				first, {{Content: "empty.go", FinishReason: "stop"}},
			}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "List Go file paths.")) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if provider.calls != 2 {
				t.Fatalf("calls=%d", provider.calls)
			}
			found := false
			for _, m := range provider.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "search_code" {
					found = true
					if m.Content != "empty.go" {
						t.Fatalf("result=%q", m.Content)
					}
				}
			}
			if !found {
				t.Fatal("missing file discovery result")
			}
		})
	}
}
