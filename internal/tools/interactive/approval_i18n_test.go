package interactive

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/system/uilang"
	core "supercli/internal/tools/core"
)

// Every UI language gets the approval prompt from the uilang catalogue, not
// only English and Polish.
func TestApprovalPromptLocalizedForEveryLanguage(t *testing.T) {
	english := map[string]string{}
	for _, key := range []string{"approval.header", "approval.approve", "approval.deny", "approval.similar", "approval.field.tool", "approval.reversible.no"} {
		english[key] = uilang.Text(uilang.English, key)
		if english[key] == key {
			t.Fatalf("missing English catalogue entry %s", key)
		}
	}
	for _, language := range uilang.Languages() {
		t.Run(language.Code, func(t *testing.T) {
			ch := make(chan AskRequest, 1)
			code := language.Code
			g := &ApprovalGate{Fallback: ch, Language: func() string { return code }}
			req := core.ApprovalRequest{Tool: "host_power", Operation: "arm_shutdown", Risk: core.RiskExternal,
				Summary: "English summary", SummaryKey: "hostPower.confirm", SummaryArgs: map[string]string{"goal": "Ship {seconds}"},
				Scope: []string{"host_power", "arm_shutdown"}}
			done := make(chan core.ApprovalDecision, 1)
			go func() { done <- g.Approve(context.Background(), req) }()
			prompt := <-ch
			want := map[string]string{
				"approval.header":  prompt.Header,
				"approval.approve": prompt.Approval.ApproveLabel,
				"approval.deny":    prompt.Approval.DenyLabel,
				"approval.similar": prompt.Approval.SimilarLabel,
			}
			for key, got := range want {
				if expected := uilang.Text(code, key); got != expected {
					t.Errorf("%s = %q, want %q", key, got, expected)
				}
				if code != uilang.English && got == english[key] {
					t.Errorf("%s is untranslated: %q", key, got)
				}
			}
			for _, key := range []string{"approval.field.tool", "approval.reversible.no"} {
				if !strings.Contains(prompt.Question, uilang.Text(code, key)) {
					t.Errorf("question lacks %s: %q", key, prompt.Question)
				}
			}
			summary := strings.ReplaceAll(uilang.Text(code, "hostPower.confirm"), "{goal}", "Ship {seconds}")
			if prompt.Approval.Summary != summary || !strings.HasPrefix(prompt.Question, summary) {
				t.Errorf("summary = %q, want %q", prompt.Approval.Summary, summary)
			}
			prompt.Respond <- AskAnswer{Selected: []string{prompt.Approval.ApproveLabel}}
			if d := <-done; !d.Approved {
				t.Fatalf("localized approve label was not accepted: %+v", d)
			}
		})
	}
}

func TestApprovalSummaryFallsBackToEnglishText(t *testing.T) {
	r := core.ApprovalRequest{Summary: "Server text", SummaryKey: "approval.summary.does_not_exist", SummaryDetail: "{}"}
	if got := localizedApprovalSummary("de", r); got != "Server text" {
		t.Fatalf("unknown key = %q", got)
	}
	r.SummaryKey = "approval.summary.headless_action"
	r.SummaryArgs = map[string]string{"target": "lab", "action": "{target}"}
	got := localizedApprovalSummary("en", r)
	if !strings.Contains(got, "“{target}”") || !strings.Contains(got, "“lab”") || !strings.HasSuffix(got, "\n{}") {
		t.Fatalf("summary = %q", got)
	}
}
