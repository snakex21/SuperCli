package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeApprover struct {
	approve bool
	calls   []ApprovalRequest
}

func (f *fakeApprover) Approve(_ context.Context, req ApprovalRequest) ApprovalDecision {
	f.calls = append(f.calls, req)
	if f.approve {
		return ApprovalDecision{Approved: true, Mode: ApprovalOnce}
	}
	return ApprovalDecision{Mode: ApprovalDenied, Reason: "the user denied it"}
}

// gatedTool records the arguments its Fn received; "op":"write" needs approval.
func gatedTool(seen *[]string, assessed *[]string) Tool {
	return Tool{
		Name:        "mailbox",
		Description: "test mailbox",
		Schema:      `{"type":"object","properties":{"op":{"type":"string"},"confirm":{"type":"boolean"}},"required":["op"]}`,
		ApprovalArg: "confirm",
		Approval: func(_ context.Context, args json.RawMessage) (*ApprovalRequest, error) {
			if assessed != nil {
				*assessed = append(*assessed, string(args))
			}
			var a struct{ Op string }
			_ = json.Unmarshal(args, &a)
			if a.Op != "write" {
				return nil, nil
			}
			return &ApprovalRequest{Operation: "write", Risk: RiskWriteReversible, Reversible: true, Summary: "write", Scope: []string{"mailbox", "write", "acct", "inbox"}}, nil
		},
		Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
			*seen = append(*seen, string(args))
			return Result{Text: "ok"}, nil
		},
	}
}

func TestApprovalGateModelConfirmWithoutApprovalIsDenied(t *testing.T) {
	SetDefaultApprover(nil)
	var seen, assessed []string
	r := NewRegistry()
	r.MustRegister(gatedTool(&seen, &assessed))
	res, err := r.Execute(context.Background(), "mailbox", json.RawMessage(`{"op":"write","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil || !errors.Is(res.Err, ErrApprovalRequired) || !strings.Contains(res.Err.Error(), "NOT executed") {
		t.Fatalf("model confirm:true must not execute without approval: %v", res.Err)
	}
	if len(seen) != 0 {
		t.Fatalf("Fn ran: %v", seen)
	}
	if strings.Contains(assessed[0], "confirm") {
		t.Fatalf("model confirm reached the risk classifier: %s", assessed[0])
	}
	if v := (Classifier{}).Classify("mailbox", nil, res); v.Category != CategoryEnvironment {
		t.Fatalf("approval refusal must not be a model retry candidate: %+v", v)
	}
}

func TestApprovalGateApprovedSetsHostConfirm(t *testing.T) {
	var seen []string
	approver := &fakeApprover{approve: true}
	r := NewRegistry()
	r.MustRegister(gatedTool(&seen, nil))
	ctx := WithApprover(context.Background(), approver)
	for _, args := range []string{`{"op":"write"}`, `{"op":"write","confirm":false}`} {
		res, _ := r.Execute(ctx, "mailbox", json.RawMessage(args))
		if res.Err != nil {
			t.Fatal(res.Err)
		}
	}
	if len(seen) != 2 || !strings.Contains(seen[0], `"confirm":true`) || !strings.Contains(seen[1], `"confirm":true`) {
		t.Fatalf("host must set confirm:true after approval: %v", seen)
	}
	if len(approver.calls) != 2 || approver.calls[0].Tool != "mailbox" || approver.calls[0].ScopeKey() != "mailbox:write:acct:inbox" {
		t.Fatalf("approver calls: %+v", approver.calls)
	}
}

func TestApprovalGateDeniedIsToolError(t *testing.T) {
	var seen []string
	r := NewRegistry()
	r.MustRegister(gatedTool(&seen, nil))
	ctx := WithApprover(context.Background(), &fakeApprover{})
	res, _ := r.Execute(ctx, "mailbox", json.RawMessage(`{"op":"write","confirm":true}`))
	// The wording "NOT executed" made models report that the tool itself
	// refused; a denial names SuperCli's prompt and the user as the source.
	if res.Err == nil || !errors.Is(res.Err, ErrApprovalRequired) {
		t.Fatalf("deny: %v", res.Err)
	}
	for _, want := range []string{"the user declined this action (mailbox write) in the SuperCli approval prompt", "nothing was done", "Don't retry", "did not refuse"} {
		if !strings.Contains(res.Err.Error(), want) {
			t.Errorf("deny text lacks %q: %v", want, res.Err)
		}
	}
	if strings.Contains(res.Err.Error(), "NOT executed") {
		t.Errorf("deny text still uses the tool-refusal wording: %v", res.Err)
	}
	err := ApprovalDeniedError(ApprovalRequest{Tool: "mailbox", Operation: "write"}, ApprovalDecision{Mode: ApprovalDenied, Reason: "the user did not answer within 10m0s"})
	if !strings.Contains(err.Error(), "because the user did not answer within 10m0s; nothing was done") || strings.Contains(err.Error(), "declined") {
		t.Errorf("timeout text: %v", err)
	}
	if len(seen) != 0 {
		t.Fatal("denied call executed")
	}
}

func TestApplyPreviewReplacesSummaryOnlyWhenSet(t *testing.T) {
	r := ApprovalRequest{Summary: "Trash in account2."}
	r.ApplyPreview(ApprovalPreview{Count: 2, AccountName: "Gmail"})
	if r.Summary != "Trash in account2." || r.Count != 2 || r.AccountName != "Gmail" {
		t.Fatalf("%+v", r)
	}
	r.ApplyPreview(ApprovalPreview{Count: -1, Summary: "Trash in Gmail.", Warnings: []string{"w"}})
	if r.Summary != "Trash in Gmail." || len(r.Warnings) != 1 || r.Count != -1 {
		t.Fatalf("%+v", r)
	}
}

func TestApprovalGateReadsRunWithoutPromptAndWithoutModelConfirm(t *testing.T) {
	var seen []string
	approver := &fakeApprover{}
	r := NewRegistry()
	r.MustRegister(gatedTool(&seen, nil))
	res, _ := r.Execute(WithApprover(context.Background(), approver), "mailbox", json.RawMessage(`{"op":"read","confirm":true}`))
	if res.Err != nil || len(approver.calls) != 0 {
		t.Fatalf("read: err=%v calls=%d", res.Err, len(approver.calls))
	}
	if strings.Contains(seen[0], "confirm") {
		t.Fatalf("model confirm must be stripped even for reads: %s", seen[0])
	}
}

func TestApprovalGatePreparedInvocationIsGated(t *testing.T) {
	SetDefaultApprover(nil)
	var seen []string
	r := NewRegistry()
	r.MustRegister(gatedTool(&seen, nil))
	inv, _, err := r.PrepareInvocation("mailbox", json.RawMessage(`{"op":"write","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	res, _ := r.ExecutePrepared(context.Background(), inv)
	if res.Err == nil || len(seen) != 0 {
		t.Fatalf("prepared path bypassed the gate: %v %v", res.Err, seen)
	}
	approver := &fakeApprover{approve: true}
	SetDefaultApprover(approver)
	defer SetDefaultApprover(nil)
	res, _ = r.ExecutePrepared(context.Background(), inv)
	if res.Err != nil || len(seen) != 1 || len(approver.calls) != 1 {
		t.Fatalf("default approver: err=%v seen=%v", res.Err, seen)
	}
}

func TestApprovalGatePreApprovedSkipsPrompt(t *testing.T) {
	var seen []string
	approver := &fakeApprover{}
	r := NewRegistry()
	tool := gatedTool(&seen, nil)
	tool.Approval = func(context.Context, json.RawMessage) (*ApprovalRequest, error) {
		return &ApprovalRequest{Operation: "write", PreApproved: "continuation", Scope: []string{"mailbox", "write"}}, nil
	}
	tool.Fn = func(ctx context.Context, args json.RawMessage) (Result, error) {
		if d, ok := ApprovalGranted(ctx); !ok || d.Mode != ApprovalPreApproved {
			t.Errorf("grant not visible to the tool: %+v", d)
		}
		seen = append(seen, string(args))
		return Result{Text: "ok"}, nil
	}
	r.MustRegister(tool)
	res, _ := r.Execute(WithApprover(context.Background(), approver), "mailbox", json.RawMessage(`{"op":"write"}`))
	if res.Err != nil || len(approver.calls) != 0 || len(seen) != 1 {
		t.Fatalf("pre-approved: err=%v calls=%d seen=%v", res.Err, len(approver.calls), seen)
	}
}

func TestApprovalDeniedErrorWithoutUINamesAllowlistScope(t *testing.T) {
	err := ApprovalDeniedError(ApprovalRequest{Tool: "thunderbird_mail", Operation: "copy", Scope: []string{"thunderbird_mail", "copy", "Account2", "a:b"}}, ApprovalDecision{Mode: ApprovalUnavailable, Reason: "batch mode (--batch)"})
	msg := err.Error()
	for _, want := range []string{"batch mode (--batch)", "auto_approve", `thunderbird_mail:copy:account2:a_b`, "NOT executed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %s", want, msg)
		}
	}
}
