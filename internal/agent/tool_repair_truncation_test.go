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

func TestTruncatedPatchDoesNotBecomeDeletion(t *testing.T) {
	for _, raw := range []string{
		`{"path":"example.go","old":"return 1\n","new":"`,
		`{"path":"example.go","old":"return 1\n","new":"return 2`,
		`{"path":"example.go","old":"return 1\n","new":"","expected_hash":`,
	} {
		t.Run(raw, func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, "example.go")
			if err := os.WriteFile(p, []byte("return 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(home).Spec())
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: home})
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "patch-1", Name: "patch_file", Arguments: raw}
			result := loop.invoke(context.Background(), call, make(chan Event, 8))
			if !result.failed || len(result.followUps) != 1 || !strings.HasPrefix(result.followUps[0].Content, badToolCallMarker) {
				t.Fatalf("unsafe repair reached execution: %+v", result)
			}
			current, err := os.ReadFile(p)
			if err != nil || string(current) != "return 1\n" {
				t.Fatalf("truncated patch changed source: %q err=%v", current, err)
			}
			// The provider replay remains structurally valid, independently of execution.
			if !json.Valid([]byte(historySafeToolArguments(raw))) {
				t.Fatal("invalid replay arguments")
			}
		})
	}
}

func TestCompletePatchDeletionAndSyntaxRepairRemainSupported(t *testing.T) {
	for _, raw := range []string{
		`{"path":"example.go","old":"return 1\n","new":""}`,
		`{"path":"example.go","old":"return 1\n","new":""`,
	} {
		t.Run(raw, func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, "example.go")
			if err := os.WriteFile(p, []byte("return 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(home).Spec())
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: home})
			if err != nil {
				t.Fatal(err)
			}
			result := loop.invoke(context.Background(), llm.ToolCall{ID: "patch-1", Name: "patch_file", Arguments: raw}, make(chan Event, 8))
			if result.failed {
				t.Fatalf("explicit complete deletion rejected: %+v", result)
			}
			current, err := os.ReadFile(p)
			if err != nil || len(current) != 0 {
				t.Fatalf("deletion did not apply: %q err=%v", current, err)
			}
		})
	}
}

func TestHardenDoesNotInventOrDiscardArgumentValues(t *testing.T) {
	for _, tc := range []llm.ToolCall{
		{Name: "create_file", Arguments: `{"path":"new.go","content":"half`},
		{Name: "read_lines", Arguments: `{"file":"wrong-prefix`},
		{Name: "goal", Arguments: `{"action":"verify","passed":t`},
		{Name: "ctx_execute", Arguments: `{"command":"echo complete","workdir":`},
	} {
		raw := tc.Arguments
		if msg := HardenToolCall(&tc, []string{tc.Name}, 0); !strings.HasPrefix(msg, badToolCallMarker) || tc.Arguments != raw {
			t.Fatalf("unsafe argument repair for %s: args=%q msg=%q", tc.Name, tc.Arguments, msg)
		}
	}
}
