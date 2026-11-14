package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func contractTool() Tool {
	return Tool{
		Name:        "contract",
		Description: "contract test tool",
		Schema:      `{"type":"object","properties":{"op":{"type":"string"},"confirm":{"type":"boolean"},"fresh":{"type":["boolean","null"]},"count":{"type":"integer"}}}`,
		Fn: func(context.Context, json.RawMessage) (Result, error) {
			return Result{Text: "ok"}, nil
		},
	}
}

func noApproval(context.Context, json.RawMessage) (*ApprovalRequest, error) { return nil, nil }

func TestValidateRegistryContract(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Tool)
		want   string // empty: valid
	}{
		{"plain schema", func(*Tool) {}, ""},
		{"empty schema rejected", func(t *Tool) { t.Schema = "" }, "Schema is empty"},
		{"blank schema rejected", func(t *Tool) { t.Schema = "  \n" }, "Schema is empty"},
		{"empty schema explicitly unchecked", func(t *Tool) { t.Schema = ""; t.UncheckedArgs = true }, ""},
		{"unchecked flag with schema", func(t *Tool) { t.UncheckedArgs = true }, "UncheckedArgs is set"},
		{"approval arg without approval", func(t *Tool) { t.ApprovalArg = "confirm" }, "without Approval"},
		{"approval arg with approval", func(t *Tool) { t.ApprovalArg = "confirm"; t.Approval = noApproval }, ""},
		{"approval arg missing from schema", func(t *Tool) { t.ApprovalArg = "confirmed"; t.Approval = noApproval }, "not a boolean property"},
		{"approval arg not boolean", func(t *Tool) { t.ApprovalArg = "op"; t.Approval = noApproval }, "not a boolean property"},
		{"approval arg on unchecked tool", func(t *Tool) {
			t.Schema, t.UncheckedArgs, t.ApprovalArg, t.Approval = "", true, "confirm", noApproval
		}, "not a boolean property"},
		{"read-only tool cannot need approval", func(t *Tool) { t.ReadOnly = true; t.Approval = noApproval }, "ReadOnly"},
		{"refresh arg boolean union", func(t *Tool) { t.RefreshArg = "fresh" }, ""},
		{"refresh arg not boolean", func(t *Tool) { t.RefreshArg = "count" }, "RefreshArg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool := contractTool()
			c.mutate(&tool)
			err := tool.Validate()
			if c.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				if regErr := NewRegistry().Register(tool); regErr != nil {
					t.Fatalf("Register() = %v", regErr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, c.want)
			}
			if NewRegistry().Register(tool) == nil {
				t.Fatal("Register accepted a tool that Validate rejects")
			}
		})
	}
}

// A tool registered with a schema always validates its arguments; only an
// explicit UncheckedArgs tool skips registry validation.
func TestEmptySchemaNeverSilentlyDisablesValidation(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(contractTool())
	res, err := r.Execute(context.Background(), "contract", json.RawMessage(`{"unknown":1}`))
	if err != nil || res.Err == nil || !errors.Is(res.Err, ErrInvalidToolArgs) {
		t.Fatalf("declared schema must reject unknown arguments: %v / %v", res.Err, err)
	}
	unchecked := contractTool()
	unchecked.Name, unchecked.Schema, unchecked.UncheckedArgs = "unchecked", "", true
	r.MustRegister(unchecked)
	if res, _ := r.Execute(context.Background(), "unchecked", json.RawMessage(`{"unknown":1}`)); res.Err != nil {
		t.Fatalf("explicitly unchecked tool: %v", res.Err)
	}
}

// Unparseable arguments for a tool with a host-owned confirmation argument
// are refused before Approval or Fn run (fail closed).
func TestApprovalArgFailsClosedOnUnparseableArgs(t *testing.T) {
	assessed := 0
	executed := 0
	tool := Tool{
		Name: "gated", Description: "gated", UncheckedArgs: true,
		Fn: func(context.Context, json.RawMessage) (Result, error) { executed++; return Result{}, nil },
		Approval: func(context.Context, json.RawMessage) (*ApprovalRequest, error) {
			assessed++
			return nil, nil
		},
	}
	// Validate requires the confirmation to be a declared boolean, so drive
	// gateInvocation directly with a tool that bypassed registration.
	tool.ApprovalArg = "confirm"
	for _, raw := range []string{`[true]`, `"confirm"`, `{"confirm":true`, `null`} {
		_, _, refused := gateInvocation(context.Background(), tool, json.RawMessage(raw))
		if refused == nil || refused.Err == nil {
			t.Fatalf("%s: not refused", raw)
		}
	}
	if assessed != 0 || executed != 0 {
		t.Fatalf("assessed=%d executed=%d", assessed, executed)
	}
}

// ApprovedRequest exposes the approved request, including its Binding, only
// inside an approved call.
func TestApprovedRequestCarriesBinding(t *testing.T) {
	if _, ok := ApprovedRequest(context.Background()); ok {
		t.Fatal("approval outside a gated call")
	}
	var got ApprovalRequest
	var ok bool
	tool := contractTool()
	tool.Approval = func(context.Context, json.RawMessage) (*ApprovalRequest, error) {
		return &ApprovalRequest{Operation: "op", Summary: "s", Scope: []string{"contract", "op"}, Binding: "exact-state"}, nil
	}
	tool.Fn = func(ctx context.Context, _ json.RawMessage) (Result, error) {
		got, ok = ApprovedRequest(ctx)
		return Result{}, nil
	}
	r := NewRegistry()
	r.MustRegister(tool)
	if _, err := r.Execute(WithApprover(context.Background(), &fakeApprover{approve: true}), "contract", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !ok || got.Binding != "exact-state" || got.Tool != "contract" {
		t.Fatalf("approved request = %+v ok=%v", got, ok)
	}
}
