package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Exercise the exact shim normalization boundary without dispatching tools.
func TestZenShimDiagnosisInboundNames(t *testing.T) {
	reg := tools.NewRegistry()
	for _, name := range []string{"ctx_execute", "read_lines"} {
		reg.MustRegister(tools.Tool{
			Name: name, Description: "synthetic stub", Schema: `{"type":"object"}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				t.Fatal("diagnosis must never execute a tool")
				return tools.Result{}, nil
			},
		})
	}
	reg.Activate("ctx_execute", "read_lines")
	loop := &Loop{registry: reg, route: RouteCoordinator}
	for _, tc := range []struct {
		name    string
		args    string
		want    string
		invalid bool
	}{
		{"bash", `{"command":"synthetic, never executed"}`, "ctx_execute", false},
		{"read", `{"filePath":"synthetic.txt","offset":2,"limit":3}`, "read_lines", false},
		{"functions.ctx_execute", `{"command":["synthetic"]}`, "ctx_execute", false},
		{"functions.read_lines", `{"file":"synthetic.txt"}`, "read_lines", false},
		{"functions.bash", `{"command":"synthetic"}`, "ctx_execute", false},
		{"tools.read", `{"filePath":"synthetic.txt"}`, "read_lines", false},
		{" read ", `{"filePath":"synthetic.txt"}`, "read_lines", false},
		{"INVALIDTOOLS", `{}`, "INVALIDTOOLS", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := llm.ToolCall{ID: "shim-fixture", Name: tc.name, Arguments: tc.args}
			call = loop.normalizeToolCallNames([]llm.ToolCall{call})[0]
			call = rewritePlaceholderToolCall(call)
			if call.Name != tc.want {
				t.Fatalf("name=%q want=%q", call.Name, tc.want)
			}
			if call.Name == "invalid_tool_call" && call.InvalidName != tc.name {
				t.Fatalf("original alias lost: %+v", call)
			}
			if tc.name == "read" {
				var args struct {
					File string `json:"file"`
					From int    `json:"from"`
					To   int    `json:"to"`
				}
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || args.File != "synthetic.txt" || args.From != 2 || args.To != 4 {
					t.Fatalf("read adaptation=%s err=%v", call.Arguments, err)
				}
			}
			if tc.name == "bash" {
				var args struct {
					Command []string `json:"command"`
				}
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || len(args.Command) != 3 || args.Command[2] != "synthetic, never executed" {
					t.Fatalf("bash adaptation=%s err=%v", call.Arguments, err)
				}
			}
			reason := HardenToolCall(&call, reg.Names(), 0)
			if (reason != "") != tc.invalid {
				t.Fatalf("invalid=%v reason=%q call=%+v", tc.invalid, reason, call)
			}
			if tc.invalid && !strings.Contains(reason, badToolCallMarker) {
				t.Fatalf("expected local tool-format classification, got %q", reason)
			}
			t.Logf("wire=%q mapped=%q invalid=%v", tc.name, call.Name, reason != "")
		})
	}
}
