package interactive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "supercli/internal/tools/core"
)

func approvalReq(folder string) core.ApprovalRequest {
	return core.ApprovalRequest{
		Tool: "thunderbird_mail", Operation: "copy", Risk: core.RiskWriteReversible, Reversible: true,
		Account: "account2", Folder: folder, Summary: "Copy",
		Scope: []string{"thunderbird_mail", "copy", "account2", folder},
	}
}

// answerWith serves one prompt on ch and records it.
func answerWith(t *testing.T, ch chan AskRequest, answer func(AskRequest) AskAnswer) <-chan AskRequest {
	t.Helper()
	got := make(chan AskRequest, 1)
	go func() {
		req := <-ch
		got <- req
		req.Respond <- answer(req)
	}()
	return got
}

func TestApprovalGateNoUIDeniesUnlessAllowAll(t *testing.T) {
	g := &ApprovalGate{NonInteractiveReason: "batch mode (--batch)"}
	d := g.Approve(context.Background(), approvalReq("inbox"))
	if d.Approved || d.Mode != core.ApprovalUnavailable || d.Reason != "batch mode (--batch)" {
		t.Fatalf("batch must deny: %+v", d)
	}
	g.AllowAllNonInteractive = true
	if d := g.Approve(context.Background(), approvalReq("inbox")); !d.Approved || d.Mode != core.ApprovalAllowAll {
		t.Fatalf("--allow-all: %+v", d)
	}
}

func TestApprovalAllowlistMatching(t *testing.T) {
	scope := "thunderbird_mail:copy:account2:account1://inbox:account2://_supercli"
	cases := []struct {
		patterns []string
		want     bool
	}{
		{[]string{"thunderbird_mail:copy:account2"}, true},
		{[]string{"thunderbird_mail:copy"}, true},
		{[]string{"thunderbird_mail:*:account2"}, true},
		{[]string{"Thunderbird_Mail:COPY:Account2"}, true},
		{[]string{"thunderbird_mail:move:account2"}, false},
		{[]string{"thunderbird_mail:copy:account1"}, false},
		{[]string{"thunderbird_mail"}, false}, // a bare tool name is too broad
		{[]string{""}, false},
	}
	for _, c := range cases {
		if _, ok := MatchApprovalAllowlist(c.patterns, scope); ok != c.want {
			t.Errorf("%v: got %v want %v", c.patterns, ok, c.want)
		}
	}
	previewed := false
	req := approvalReq("account1://INBOX")
	req.Preview = func(context.Context) core.ApprovalPreview { previewed = true; return core.ApprovalPreview{} }
	g := &ApprovalGate{AutoApprove: []string{"thunderbird_mail:copy:account2"}}
	if d := g.Approve(context.Background(), req); !d.Approved || d.Mode != core.ApprovalAllowlist {
		t.Fatalf("allowlist in batch: %+v", d)
	}
	if previewed {
		t.Fatal("allowlisted request must not query a preview")
	}
}

func TestApprovalGatePromptApproveDenyCustomTimeout(t *testing.T) {
	ch := make(chan AskRequest, 1)
	g := &ApprovalGate{Fallback: ch}
	req := approvalReq("inbox")
	req.Preview = func(context.Context) core.ApprovalPreview {
		return core.ApprovalPreview{AccountName: "Local Folders", Count: 2, Samples: []core.ApprovalSample{{From: "a@example.test", Subject: "Hello"}}}
	}
	got := answerWith(t, ch, func(r AskRequest) AskAnswer { return AskAnswer{Selected: []string{r.Approval.ApproveLabel}} })
	if d := g.Approve(context.Background(), req); !d.Approved || d.Mode != core.ApprovalOnce {
		t.Fatalf("approve: %+v", d)
	}
	prompt := <-got
	if !prompt.Confirmation || prompt.Approval == nil || prompt.AllowCustom || len(prompt.Options) != 3 {
		t.Fatalf("prompt shape: %+v", prompt)
	}
	if prompt.Approval.AccountName != "Local Folders" || prompt.Approval.Count != 2 || len(prompt.Approval.Samples) != 1 {
		t.Fatalf("preview missing from card: %+v", prompt.Approval)
	}
	if !strings.Contains(prompt.Question, "Local Folders (account2)") || !strings.Contains(prompt.Question, "Hello") || !strings.Contains(prompt.Question, "Reversible: yes") {
		t.Fatalf("question text: %s", prompt.Question)
	}
	for name, answer := range map[string]func(AskRequest) AskAnswer{
		"deny":   func(r AskRequest) AskAnswer { return AskAnswer{Selected: []string{r.Approval.DenyLabel}} },
		"cancel": func(AskRequest) AskAnswer { return AskAnswer{Cancelled: true} },
		"custom": func(r AskRequest) AskAnswer {
			return AskAnswer{Selected: []string{r.Approval.ApproveLabel}, Custom: "yes do it"}
		},
		"forged": func(AskRequest) AskAnswer { return AskAnswer{Selected: []string{"Allow once"}} },
	} {
		answerWith(t, ch, answer)
		if d := g.Approve(context.Background(), approvalReq("inbox")); d.Approved {
			t.Fatalf("%s approved: %+v", name, d)
		}
	}
	g.Timeout = 20 * time.Millisecond
	go func() { <-ch }() // UI receives but never answers
	if d := g.Approve(context.Background(), approvalReq("inbox")); d.Approved || !strings.Contains(d.Reason, "did not answer") {
		t.Fatalf("timeout: %+v", d)
	}
}

func TestApprovalGateContextChannelWinsOverFallback(t *testing.T) {
	fallback := make(chan AskRequest, 1)
	run := make(chan AskRequest, 1)
	g := &ApprovalGate{Fallback: fallback}
	answerWith(t, run, func(r AskRequest) AskAnswer { return AskAnswer{Selected: []string{r.Approval.ApproveLabel}} })
	ctx := WithAskChannel(context.Background(), run)
	if d := g.Approve(ctx, approvalReq("inbox")); !d.Approved {
		t.Fatalf("run channel: %+v", d)
	}
	if len(fallback) != 0 {
		t.Fatal("prompt went to the stale fallback channel")
	}
}

func TestApprovalGateSessionGrantScopeAndExpiry(t *testing.T) {
	ch := make(chan AskRequest, 1)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	g := &ApprovalGate{Fallback: ch, GrantTTL: 30 * time.Minute, Now: func() time.Time { return now }}
	ctx := core.WithApprovalSession(context.Background(), "chat-1")
	answerWith(t, ch, func(r AskRequest) AskAnswer { return AskAnswer{Selected: []string{r.Approval.SimilarLabel}} })
	if d := g.Approve(ctx, approvalReq("inbox")); !d.Approved || d.Mode != core.ApprovalSimilar {
		t.Fatalf("similar: %+v", d)
	}
	if d := g.Approve(ctx, approvalReq("inbox")); !d.Approved || d.Mode != core.ApprovalSessionGrant {
		t.Fatalf("same scope must reuse the grant: %+v", d)
	}
	// Different folder, different session: prompt again (deny it).
	for _, c := range []struct {
		ctx    context.Context
		folder string
	}{{ctx, "sent"}, {core.WithApprovalSession(context.Background(), "chat-2"), "inbox"}} {
		got := answerWith(t, ch, func(AskRequest) AskAnswer { return AskAnswer{Cancelled: true} })
		if d := g.Approve(c.ctx, approvalReq(c.folder)); d.Approved {
			t.Fatalf("grant leaked to %s: %+v", c.folder, d)
		}
		<-got
	}
	now = now.Add(31 * time.Minute)
	got := answerWith(t, ch, func(AskRequest) AskAnswer { return AskAnswer{Cancelled: true} })
	if d := g.Approve(ctx, approvalReq("inbox")); d.Approved {
		t.Fatalf("expired grant still approves: %+v", d)
	}
	<-got
}

func TestApprovalGateIrreversibleOffersNoSessionGrant(t *testing.T) {
	ch := make(chan AskRequest, 1)
	g := &ApprovalGate{Fallback: ch}
	req := approvalReq("trash")
	req.Operation, req.Reversible, req.NoSessionGrant, req.Risk = "empty_trash", false, true, core.RiskDestructive
	got := answerWith(t, ch, func(AskRequest) AskAnswer { return AskAnswer{Selected: []string{"Approve similar in this session"}} })
	if d := g.Approve(context.Background(), req); d.Approved {
		t.Fatalf("session grant accepted for irreversible op: %+v", d)
	}
	prompt := <-got
	if len(prompt.Options) != 2 || prompt.Approval.SimilarLabel != "" || !strings.Contains(prompt.Question, "cannot be undone") {
		t.Fatalf("irreversible prompt: %+v / %s", prompt.Options, prompt.Question)
	}
}

func TestApprovalGatePolishLabelsAndAuditLog(t *testing.T) {
	ch := make(chan AskRequest, 1)
	audit := filepath.Join(t.TempDir(), "approvals.jsonl")
	g := &ApprovalGate{Fallback: ch, AuditPath: audit, Language: func() string { return "pl" }}
	req := approvalReq("inbox")
	req.Preview = func(context.Context) core.ApprovalPreview {
		return core.ApprovalPreview{Count: 1, Samples: []core.ApprovalSample{{Subject: "private subject"}}}
	}
	got := answerWith(t, ch, func(r AskRequest) AskAnswer { return AskAnswer{Selected: []string{"Zatwierdź"}} })
	if d := g.Approve(core.WithApprovalSession(context.Background(), "s1"), req); !d.Approved {
		t.Fatalf("polish approve: %+v", d)
	}
	prompt := <-got
	if prompt.Header != "Wymagana zgoda" || prompt.Options[0].Label != "Odrzuć" || prompt.Options[2].Label != "Zatwierdź podobne w tej sesji" {
		t.Fatalf("polish prompt: %+v", prompt)
	}
	g.Fallback = nil
	g.Approve(context.Background(), approvalReq("inbox"))
	data, err := os.ReadFile(audit)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || strings.Contains(string(data), "private subject") {
		t.Fatalf("audit log: %s", data)
	}
	var first ApprovalAuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if !first.Approved || first.Mode != core.ApprovalOnce || first.Session != "s1" || first.Scope != "thunderbird_mail:copy:account2:inbox" || first.Count != 1 {
		t.Fatalf("audit entry: %+v", first)
	}
	if entries := g.AuditEntries(); len(entries) != 2 || entries[1].Approved || entries[1].Mode != core.ApprovalUnavailable {
		t.Fatalf("in-memory audit: %+v", entries)
	}
}
