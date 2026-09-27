package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestToolFingerprintPreservesArgumentValues(t *testing.T) {
	for _, tc := range []struct{ name, a, b string }{
		{"object versus pairs", `{"data":{"x":1}}`, `{"data":[["x",1]]}`},
		{"empty object versus array", `{"data":{}}`, `{"data":[]}`},
		{"nested object versus pairs", `{"changes":[{"new":"after","old":"before"}]}`, `{"changes":[[["new","after"],["old","before"]]]}`},
		{"integer precision", `{"id":9007199254740992}`, `{"id":9007199254740993}`},
		{"decimal precision", `{"value":0.12345678901234567891}`, `{"value":0.12345678901234567892}`},
		{"array order", `{"items":[1,2]}`, `{"items":[2,1]}`},
		{"number versus string", `{"id":1}`, `{"id":"1"}`},
		{"null versus empty", `{"data":null}`, `{"data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if toolCallFingerprint("patch_file", tc.a) == toolCallFingerprint("patch_file", tc.b) {
				t.Fatal("distinct arguments share a repeat fingerprint")
			}
			var fails identicalFailureGate
			fails.recordFailure("patch_file", tc.a)
			fails.recordFailure("patch_file", tc.a)
			if fails.shouldBlock("patch_file", tc.b) {
				t.Fatal("changed arguments blocked by old failures")
			}
			var writes identicalSuccessGate
			writes.recordSuccess("patch_file", tc.a)
			writes.recordSuccess("patch_file", tc.a)
			if writes.shouldBlock("patch_file", tc.b) {
				t.Fatal("distinct mutation blocked by previous writes")
			}
		})
	}
}

func TestToolFingerprintCanonicalJSON(t *testing.T) {
	a := `{"data":{"b":[{"z":2,"a":1}],"a":true},"id":9007199254740993,"base_hash":"old"}`
	b := ` {"base_hash":"new","id":9007199254740993,"data":{"a":true,"b":[{"a":1,"z":2}]}} `
	if toolCallFingerprint("patch_file", a) != toolCallFingerprint("patch_file", b) {
		t.Fatal("key order or advisory hash changed identity")
	}
	normalized := normalizeToolArgsJSON(a)
	if normalizeToolArgsJSON(normalized) != normalized {
		t.Fatal("normalization is not idempotent")
	}
	for _, malformed := range []string{`{"x":1} {"x":2}`, `{"x":1} garbage`, `{"x":`} {
		if got := normalizeToolArgsJSON(malformed); got != malformed {
			t.Fatalf("malformed arguments lost evidence: %q", got)
		}
	}
	for _, empty := range []string{"", "  ", "null", "{}"} {
		if got := normalizeToolArgsJSON(empty); got != "{}" {
			t.Errorf("empty args %q normalized to %q", empty, got)
		}
	}
}

// Real patch recovery: two invalid array-shaped entries must not poison the
// corrected object-shaped entry. The former normalizer encoded both alike.
func TestPatchShapeRepairNotBlocked(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "sample.txt")
			if err := os.WriteFile(target, []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(root).Spec())
			reg.MarkAlwaysOn("patch_file")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg, BaseDir: root, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			bad := `{"path":"sample.txt","changes":[[["new","after"],["old","before"]]]}`
			good := `{"path":"sample.txt","changes":[{"old":"before","new":"after"}]}`
			events := make(chan Event, 64)
			for i, args := range []string{bad, bad, good} {
				call := llm.ToolCall{ID: fmt.Sprintf("repair-%d", i), Name: "patch_file", Arguments: args}
				if thin {
					calls, _ := extractSentinelToolCalls("«invoke_tool\ntool: patch_file\nargs: " + args + "\n»")
					if len(calls) != 1 {
						t.Fatal("sentinel did not produce one call")
					}
					call = calls[0]
				}
				result := loop.invoke(context.Background(), call, events)
				if len(result.followUps) != 1 {
					t.Fatalf("missing result: %+v", result)
				}
				if i < 2 {
					if !result.failed || !strings.Contains(result.followUps[0].Content, "expected object") {
						t.Fatalf("expected schema rejection, got %+v", result)
					}
					actual, err := os.ReadFile(target)
					if err != nil || string(actual) != "before\n" {
						t.Fatal("invalid call changed file")
					}
				} else if result.failed {
					t.Fatalf("corrected patch failed: %s", result.followUps[0].Content)
				}
			}
			actual, err := os.ReadFile(target)
			if err != nil || string(actual) != "after\n" {
				t.Fatalf("repair not applied: %q, %v", actual, err)
			}
		})
	}
}

var argumentFingerprintSink [32]byte

func BenchmarkToolArgumentFingerprint(b *testing.B) {
	changes := make([]map[string]any, 20)
	for i := range changes {
		changes[i] = map[string]any{"old": fmt.Sprintf("line %d before", i), "new": fmt.Sprintf("line %d after", i), "expected_count": 1}
	}
	patch, _ := json.Marshal(map[string]any{"path": "sample.txt", "changes": changes, "base_hash": strings.Repeat("a", 64)})
	for _, tc := range []struct{ name, args string }{
		{"read", `{"file":"sample.go","from":20,"to":80}`},
		{"patch20", string(patch)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				argumentFingerprintSink = toolCallFingerprint("patch_file", tc.args)
			}
		})
	}
}
