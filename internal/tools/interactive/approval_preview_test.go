package interactive

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/system/uilang"
	core "supercli/internal/tools/core"
)

// The preview may rename the account in the summary and add warnings; both
// reach the card and the plain-text question.
func TestApprovalGateShowsPreviewSummaryAndWarnings(t *testing.T) {
	ch := make(chan AskRequest, 1)
	g := &ApprovalGate{Fallback: ch, Language: func() string { return "pl" }}
	req := core.ApprovalRequest{Tool: "thunderbird_mail", Operation: "trash", Risk: core.RiskWriteReversible, Reversible: true,
		Account: "account2", Summary: "Move matching messages from \"INBOX\" in account2 to Trash.",
		Scope: []string{"thunderbird_mail", "trash", "account2", "INBOX"},
		Preview: func(context.Context) core.ApprovalPreview {
			return core.ApprovalPreview{AccountName: "Gmail", Count: 1, Summary: "Move matching messages from \"INBOX\" in Gmail to Trash.",
				Warnings: []string{"Not in \"INBOX\": message 7."}}
		}}
	done := make(chan core.ApprovalDecision, 1)
	go func() { done <- g.Approve(context.Background(), req) }()
	prompt := <-ch
	if prompt.Approval.Summary != "Move matching messages from \"INBOX\" in Gmail to Trash." || strings.Contains(prompt.Question, "account2 to") {
		t.Fatalf("summary %q / question %q", prompt.Approval.Summary, prompt.Question)
	}
	if len(prompt.Approval.Warnings) != 1 || !strings.Contains(prompt.Question, uilang.Text("pl", "approval.field.warnings")+": Not in \"INBOX\": message 7.") {
		t.Fatalf("warnings %v / question %q", prompt.Approval.Warnings, prompt.Question)
	}
	prompt.Respond <- AskAnswer{Cancelled: true}
	d := <-done
	if d.Approved || d.Reason != core.ApprovalReasonUserDenied {
		t.Fatalf("decision %+v", d)
	}
	err := core.ApprovalDeniedError(req, d)
	if !strings.Contains(err.Error(), "the user declined this action") || !strings.Contains(err.Error(), "thunderbird_mail tool did not refuse") {
		t.Fatalf("denied error %v", err)
	}
}

// The trash-outside-workspace summary is localized in every UI language and
// keeps the path and the .supercli/trash literal.
func TestTrashOutsideWorkspaceSummaryLocalized(t *testing.T) {
	r := core.ApprovalRequest{Summary: "English", SummaryKey: "approval.summary.trash_outside_workspace",
		SummaryArgs: map[string]string{"path": `C:\Users\me\{x}.txt`}}
	english := localizedApprovalSummary(uilang.English, r)
	for _, language := range uilang.Languages() {
		got := localizedApprovalSummary(language.Code, r)
		if got == "English" || !strings.Contains(got, `C:\Users\me\{x}.txt`) || !strings.Contains(got, ".supercli/trash") {
			t.Errorf("%s: %q", language.Code, got)
		}
		if language.Code != uilang.English && got == english {
			t.Errorf("%s is untranslated", language.Code)
		}
		if label := uilang.Text(language.Code, "approval.field.warnings"); label == "approval.field.warnings" {
			t.Errorf("%s lacks approval.field.warnings", language.Code)
		}
	}
}
