package mail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	core "supercli/internal/tools/core"
)

type recordingApprover struct {
	approve bool
	reqs    []core.ApprovalRequest
}

func (r *recordingApprover) Approve(_ context.Context, req core.ApprovalRequest) core.ApprovalDecision {
	r.reqs = append(r.reqs, req)
	if r.approve {
		return core.ApprovalDecision{Approved: true, Mode: core.ApprovalOnce}
	}
	return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: "the user denied it"}
}

func TestThunderbirdEveryWriteOpDeclaresRiskAndReadsDoNot(t *testing.T) {
	tool := NewThunderbirdMail()
	var spec struct {
		Properties struct {
			Op struct {
				Enum []string `json:"enum"`
			} `json:"op"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(tool.Spec().Schema), &spec); err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{"status": true, "accounts": true, "folders": true, "count": true, "search": true, "read": true,
		"attachments": true, "get_attachment": true, "senders": true, "largest": true, "verify_move": true,
		"address_books": true, "contacts": true, "contact_candidates": true}
	for _, op := range spec.Properties.Op.Enum {
		req, err := tool.approval(context.Background(), json.RawMessage(`{"op":"`+op+`","account":"account2","folder":"account2://X"}`))
		if err != nil {
			t.Fatal(err)
		}
		if reads[op] {
			if req != nil {
				t.Errorf("read op %s asks for approval", op)
			}
			continue
		}
		if req == nil {
			t.Errorf("write op %s is not gated", op)
			continue
		}
		if _, ok := thunderbirdOpRisk[op]; !ok || req.Risk == "" || req.Summary == "" || req.Scope[0] != "thunderbird_mail" || req.Scope[1] != op {
			t.Errorf("op %s request: %+v", op, req)
		}
		if req.Reversible == req.NoSessionGrant {
			t.Errorf("op %s: only reversible ops may offer a session grant", op)
		}
	}
}

func TestThunderbirdApprovalDescribesAccountFolderAndDestination(t *testing.T) {
	tool := NewThunderbirdMail()
	req, _ := tool.approval(context.Background(), json.RawMessage(`{"op":"copy","folder":"account1://INBOX","destination":"_SuperCliTest","destination_account":"account2","subject":"x","confirm":true}`))
	if req.Account != "account1" || req.Folder != "account1://INBOX" || req.Destination != "account2/_SuperCliTest" || !req.Reversible {
		t.Fatalf("copy request: %+v", req)
	}
	if got := req.ScopeKey(); got != "thunderbird_mail:copy:account1:account1_//inbox:account2/_superclitest" {
		t.Fatalf("scope %s", got)
	}
	req, _ = tool.approval(context.Background(), json.RawMessage(`{"op":"create_folder","account":"Local Folders","name":"_SuperCliTest"}`))
	if req.Account != "Local Folders" || req.Target != "_SuperCliTest" || !strings.Contains(req.Summary, "_SuperCliTest") {
		t.Fatalf("create_folder request: %+v", req)
	}
	req, _ = tool.approval(context.Background(), json.RawMessage(`{"op":"empty_trash","account":"account1"}`))
	if req.Reversible || !req.NoSessionGrant || req.Risk != core.RiskDestructive {
		t.Fatalf("empty_trash must be irreversible: %+v", req)
	}
}

// The live failure this gate exists for: a model sent confirm:true by itself.
func TestThunderbirdModelConfirmIsIgnoredWithoutUserApproval(t *testing.T) {
	core.SetDefaultApprover(nil)
	r := core.NewRegistry()
	r.MustRegister(NewThunderbirdMail(t.TempDir()).Spec())
	res, err := r.Execute(context.Background(), "thunderbird_mail", json.RawMessage(`{"op":"create_folder","account":"account1","name":"Oops","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "NOT executed") || !strings.Contains(res.Err.Error(), "thunderbird_mail create_folder") {
		t.Fatalf("expected refusal, got %v / %s", res.Err, res.Text)
	}
	denier := &recordingApprover{}
	res, _ = r.Execute(core.WithApprover(context.Background(), denier), "thunderbird_mail", json.RawMessage(`{"op":"delete_folder","folder":"account2://_SuperCliTest","confirm":true}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "the user denied it") || len(denier.reqs) != 1 {
		t.Fatalf("deny: %v (%d prompts)", res.Err, len(denier.reqs))
	}
}

func TestThunderbirdApprovedCallReceivesHostConfirm(t *testing.T) {
	tool := NewThunderbirdMail(t.TempDir()).Spec()
	var got []string
	tool.Fn = func(_ context.Context, args json.RawMessage) (Result, error) {
		got = append(got, string(args))
		return Result{Text: "{}"}, nil
	}
	r := core.NewRegistry()
	r.MustRegister(tool)
	approver := &recordingApprover{approve: true}
	ctx := core.WithApprover(context.Background(), approver)
	res, _ := r.Execute(ctx, "thunderbird_mail", json.RawMessage(`{"op":"create_folder","account":"account2","name":"_SuperCliTest"}`))
	if res.Err != nil || len(got) != 1 || !strings.Contains(got[0], `"confirm":true`) {
		t.Fatalf("approved call: err=%v args=%v", res.Err, got)
	}
	res, _ = r.Execute(ctx, "thunderbird_mail", json.RawMessage(`{"op":"folders","confirm":true}`))
	if res.Err != nil || strings.Contains(got[1], "confirm") || len(approver.reqs) != 1 {
		t.Fatalf("read: err=%v args=%s prompts=%d", res.Err, got[1], len(approver.reqs))
	}
}

func TestThunderbirdContinuationOfApprovedBatchIsPreApprovedOnce(t *testing.T) {
	tool := NewThunderbirdMail()
	args := json.RawMessage(`{"op":"trash","from":"shop","continuation":"trash-abc"}`)
	if req, _ := tool.approval(context.Background(), args); req.PreApproved != "" {
		t.Fatal("unknown continuation token must not be pre-approved")
	}
	// A token returned to an unapproved call is never remembered.
	thunderbirdRememberContinuation(context.Background(), json.RawMessage(`{"continuation":"trash-abc","more":true}`))
	if req, _ := tool.approval(context.Background(), args); req.PreApproved != "" {
		t.Fatal("token from an unapproved call was trusted")
	}
	r := core.NewRegistry()
	spec := tool.Spec()
	spec.Fn = func(ctx context.Context, _ json.RawMessage) (Result, error) {
		thunderbirdRememberContinuation(ctx, json.RawMessage(`{"continuation":"trash-abc","more":true}`))
		return Result{Text: "{}"}, nil
	}
	r.MustRegister(spec)
	r.Execute(core.WithApprover(context.Background(), &recordingApprover{approve: true}), "thunderbird_mail", json.RawMessage(`{"op":"trash","from":"shop"}`))
	req, _ := tool.approval(context.Background(), args)
	if req.PreApproved == "" {
		t.Fatal("continuation of the approved batch must not ask again")
	}
	if req, _ := tool.approval(context.Background(), args); req.PreApproved != "" {
		t.Fatal("a continuation token is single use")
	}
}

func TestOutlookTrashAndPurgeAreGated(t *testing.T) {
	for op, want := range map[string]bool{"trash": true, "purge": true, "search": false, "draft": false, "export_msg": false} {
		req, _ := outlookApproval(context.Background(), json.RawMessage(`{"op":"`+op+`","from":"shop","confirm":true}`))
		if (req != nil) != want {
			t.Errorf("%s gated=%v want %v", op, req != nil, want)
		}
	}
	req, _ := outlookApproval(context.Background(), json.RawMessage(`{"op":"purge","subject":"x"}`))
	if req.Reversible || !req.NoSessionGrant || req.Target != "subject=x" {
		t.Fatalf("purge: %+v", req)
	}
	spec := NewOutlookMail().Spec()
	if spec.Approval == nil || spec.ApprovalArg != "confirm" {
		t.Fatal("outlook_mail spec is not wired to the gate")
	}
}
