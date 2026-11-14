package interactive

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"supercli/internal/system/uilang"
	core "supercli/internal/tools/core"
)

// ApprovalCard is the trusted, structured content of an approval prompt. UIs
// that know it (SuperCli GUI and TUI) render a dedicated card; other overlays
// (for example a branded launcher) still get Question text and option labels.
type ApprovalCard struct {
	Tool        string         `json:"tool"`
	Operation   string         `json:"operation"`
	Risk        core.RiskLevel `json:"risk"`
	Reversible  bool           `json:"reversible"`
	Account     string         `json:"account,omitempty"`
	AccountName string         `json:"account_name,omitempty"`
	Folder      string         `json:"folder,omitempty"`
	Destination string         `json:"destination,omitempty"`
	Target      string         `json:"target,omitempty"`
	Summary     string         `json:"summary"`
	// SummaryKey/SummaryArgs/SummaryDetail let a UI re-localize Summary
	// when its language changes; Summary is already in the gate language.
	SummaryKey    string                `json:"summary_key,omitempty"`
	SummaryArgs   map[string]string     `json:"summary_args,omitempty"`
	SummaryDetail string                `json:"summary_detail,omitempty"`
	Count         int                   `json:"count"`
	Samples       []core.ApprovalSample `json:"samples,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	Scope         string                `json:"scope"`
	ApproveLabel  string                `json:"approve_label"`
	DenyLabel     string                `json:"deny_label"`
	SimilarLabel  string                `json:"similar_label,omitempty"`
	GrantMinutes  int                   `json:"grant_minutes,omitempty"`
}

// ApprovalAuditEntry is one line of the approvals audit log. It carries no
// message subjects or bodies.
type ApprovalAuditEntry struct {
	Time     time.Time      `json:"time"`
	Session  string         `json:"session,omitempty"`
	Tool     string         `json:"tool"`
	Op       string         `json:"op"`
	Scope    string         `json:"scope"`
	Risk     core.RiskLevel `json:"risk"`
	Approved bool           `json:"approved"`
	Mode     string         `json:"mode"`
	Reason   string         `json:"reason,omitempty"`
	Count    int            `json:"count,omitempty"`
}

// ApprovalGate is the host approver: config allowlist, session grants, then a
// trusted UI prompt; with no UI it denies unless the run explicitly allows all.
type ApprovalGate struct {
	// Fallback is the TUI ask channel; GUI runs route through the context.
	Fallback chan<- AskRequest
	// AutoApprove holds [approvals] auto_approve scope patterns.
	AutoApprove []string
	// AllowAllNonInteractive approves requests when no UI is connected
	// (only the explicit --allow-all flag of a batch run sets it).
	AllowAllNonInteractive bool
	// NonInteractiveReason names the run kind in denial messages.
	NonInteractiveReason string
	// GrantTTL bounds "approve similar in this session"; default 30 minutes.
	GrantTTL time.Duration
	// Timeout bounds one prompt; default 10 minutes, then it is a denial.
	Timeout time.Duration
	// AuditPath, when set, receives one JSON line per decision.
	AuditPath string
	// Language returns the UI language for prompt text ("pl", "en", ...).
	Language func() string
	// Now is injectable for tests.
	Now func() time.Time

	mu     sync.Mutex
	grants map[string]time.Time
	audit  []ApprovalAuditEntry
}

const approvalAuditKeep = 200

func (g *ApprovalGate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *ApprovalGate) grantTTL() time.Duration {
	if g.GrantTTL > 0 {
		return g.GrantTTL
	}
	return 30 * time.Minute
}

// Approve implements core.Approver.
func (g *ApprovalGate) Approve(ctx context.Context, req core.ApprovalRequest) core.ApprovalDecision {
	d := g.decide(ctx, &req)
	g.Record(ctx, req, d)
	return d
}

func (g *ApprovalGate) decide(ctx context.Context, req *core.ApprovalRequest) core.ApprovalDecision {
	scope := req.ScopeKey()
	if pattern, ok := MatchApprovalAllowlist(g.AutoApprove, scope); ok {
		return core.ApprovalDecision{Approved: true, Mode: core.ApprovalAllowlist, Reason: "config [approvals] auto_approve " + pattern}
	}
	grantKey := core.ApprovalSession(ctx) + "|" + scope
	g.mu.Lock()
	expiry, granted := g.grants[grantKey]
	if granted && !g.now().Before(expiry) {
		delete(g.grants, grantKey)
		granted = false
	}
	g.mu.Unlock()
	if granted && !req.NoSessionGrant {
		return core.ApprovalDecision{Approved: true, Mode: core.ApprovalSessionGrant, Reason: "similar operation approved earlier in this session"}
	}
	in := g.Fallback
	if current, ok := ctx.Value(askChannelKey{}).(chan<- AskRequest); ok {
		in = current
	}
	if in == nil {
		if g.AllowAllNonInteractive {
			return core.ApprovalDecision{Approved: true, Mode: core.ApprovalAllowAll, Reason: "--allow-all in a non-interactive run"}
		}
		reason := g.NonInteractiveReason
		if reason == "" {
			reason = "non-interactive run"
		}
		return core.ApprovalDecision{Mode: core.ApprovalUnavailable, Reason: reason}
	}
	if req.Preview != nil {
		previewCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		p := req.Preview(previewCtx)
		cancel()
		req.ApplyPreview(p)
	} else {
		req.Count = -1
	}
	return g.ask(ctx, in, *req, grantKey)
}

func (g *ApprovalGate) language() string {
	if g.Language == nil {
		return uilang.English
	}
	return uilang.Normalize(g.Language())
}

type approvalTexts struct {
	lang                           string
	header, approve, deny, similar string
}

// texts returns the prompt labels in the UI language. All UI languages come
// from the uilang catalogue; a missing key falls back to English there.
func (g *ApprovalGate) texts() approvalTexts {
	lang := g.language()
	return approvalTexts{
		lang:    lang,
		header:  uilang.Text(lang, "approval.header"),
		approve: uilang.Text(lang, "approval.approve"),
		deny:    uilang.Text(lang, "approval.deny"),
		similar: uilang.Text(lang, "approval.similar"),
	}
}

func (g *ApprovalGate) ask(ctx context.Context, in chan<- AskRequest, req core.ApprovalRequest, grantKey string) core.ApprovalDecision {
	tx := g.texts()
	card := &ApprovalCard{
		Tool: req.Tool, Operation: req.Operation, Risk: req.Risk, Reversible: req.Reversible,
		Account: req.Account, AccountName: req.AccountName, Folder: req.Folder, Destination: req.Destination,
		Target: req.Target, Summary: localizedApprovalSummary(tx.lang, req),
		SummaryKey: req.SummaryKey, SummaryArgs: req.SummaryArgs, SummaryDetail: req.SummaryDetail, Count: req.Count, Samples: req.Samples, Warnings: req.Warnings, Scope: req.ScopeKey(),
		ApproveLabel: tx.approve, DenyLabel: tx.deny,
	}
	options := []AskOption{{Label: tx.deny}, {Label: tx.approve}}
	if !req.NoSessionGrant {
		card.SimilarLabel = tx.similar
		card.GrantMinutes = int(g.grantTTL() / time.Minute)
		options = append(options, AskOption{Label: tx.similar})
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	done := make(chan struct{})
	defer close(done)
	response := make(chan AskAnswer, 1)
	request := AskRequest{ID: newAskID(), Header: tx.header, Question: approvalQuestion(tx.lang, req), Confirmation: true, Approval: card, Options: options, Respond: response, Done: done}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case in <- request:
	case <-ctx.Done():
		return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: "the run was cancelled before the user answered"}
	case <-timer.C:
		return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: "the approval prompt could not be shown"}
	}
	select {
	case answer := <-response:
		if answer.Cancelled || answer.Custom != "" || len(answer.Selected) != 1 {
			return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: core.ApprovalReasonUserDenied}
		}
		switch answer.Selected[0] {
		case tx.approve:
			return core.ApprovalDecision{Approved: true, Mode: core.ApprovalOnce, Reason: "approved by the user"}
		case tx.similar:
			if req.NoSessionGrant {
				break
			}
			g.mu.Lock()
			if g.grants == nil {
				g.grants = map[string]time.Time{}
			}
			g.grants[grantKey] = g.now().Add(g.grantTTL())
			g.mu.Unlock()
			return core.ApprovalDecision{Approved: true, Mode: core.ApprovalSimilar, Reason: "approved by the user for similar operations in this session"}
		}
		return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: core.ApprovalReasonUserDenied}
	case <-ctx.Done():
		return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: "the run was cancelled before the user answered"}
	case <-timer.C:
		return core.ApprovalDecision{Mode: core.ApprovalDenied, Reason: fmt.Sprintf("the user did not answer within %v", timeout)}
	}
}

// Record appends a decision to the in-memory and on-disk audit log.
func (g *ApprovalGate) Record(ctx context.Context, req core.ApprovalRequest, d core.ApprovalDecision) {
	entry := ApprovalAuditEntry{
		Time: g.now().UTC(), Session: core.ApprovalSession(ctx), Tool: req.Tool, Op: req.Operation,
		Scope: req.ScopeKey(), Risk: req.Risk, Approved: d.Approved, Mode: d.Mode, Reason: d.Reason,
	}
	if req.Count > 0 {
		entry.Count = req.Count
	}
	g.mu.Lock()
	g.audit = append(g.audit, entry)
	if len(g.audit) > approvalAuditKeep {
		g.audit = append([]ApprovalAuditEntry(nil), g.audit[len(g.audit)-approvalAuditKeep:]...)
	}
	path := g.AuditPath
	g.mu.Unlock()
	log.Printf("approval: %s %s scope=%s approved=%v mode=%s", req.Tool, req.Operation, entry.Scope, d.Approved, d.Mode)
	if path == "" {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("approval: audit log: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// AuditEntries returns a copy of the recent decisions, oldest first.
func (g *ApprovalGate) AuditEntries() []ApprovalAuditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ApprovalAuditEntry(nil), g.audit...)
}

// MatchApprovalAllowlist reports the first pattern matching scope. A pattern
// is "tool:op[:account[:folder[:destination]]]"; "*" matches any one segment
// and a shorter pattern matches every longer scope it prefixes.
func MatchApprovalAllowlist(patterns []string, scope string) (string, bool) {
	have := strings.Split(scope, ":")
	for _, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		want := strings.Split(strings.ToLower(pattern), ":")
		if len(want) < 2 || len(want) > len(have) {
			continue
		}
		ok := true
		for i, w := range want {
			w = strings.TrimSpace(w)
			if w != "*" && w != have[i] {
				ok = false
				break
			}
		}
		if ok {
			return pattern, true
		}
	}
	return "", false
}

// localizedApprovalSummary renders the trusted SummaryKey in lang. The
// placeholders are substituted in one pass, so argument text containing
// "{name}" stays literal. Without a catalogue entry the English Summary stays.
func localizedApprovalSummary(lang string, r core.ApprovalRequest) string {
	if r.SummaryKey == "" {
		return r.Summary
	}
	text := uilang.Text(lang, r.SummaryKey)
	if text == r.SummaryKey {
		return r.Summary
	}
	pairs := make([]string, 0, 2*len(r.SummaryArgs))
	for name, value := range r.SummaryArgs {
		pairs = append(pairs, "{"+name+"}", value)
	}
	text = strings.NewReplacer(pairs...).Replace(text)
	if r.SummaryDetail != "" {
		text += "\n" + r.SummaryDetail
	}
	return text
}

// approvalQuestion is the plain-text prompt for UIs without a dedicated card
// (TUI details, branded overlays). Labels come from the uilang catalogue.
func approvalQuestion(lang string, r core.ApprovalRequest) string {
	label := func(key string) string { return uilang.Text(lang, key) + ": " }
	var b strings.Builder
	b.WriteString(localizedApprovalSummary(lang, r))
	for _, w := range r.Warnings {
		b.WriteString("\n\n! " + label("approval.field.warnings") + w)
	}
	b.WriteString("\n\n" + label("approval.field.tool") + r.Tool + " / " + r.Operation)
	if account := approvalAccountLabel(r); account != "" {
		b.WriteString("\n" + label("approval.field.account") + account)
	}
	if r.Folder != "" {
		b.WriteString("\n" + label("approval.field.folder") + r.Folder)
	}
	if r.Destination != "" {
		b.WriteString("\n" + label("approval.field.destination") + r.Destination)
	}
	if r.Target != "" {
		b.WriteString("\n" + label("approval.field.target") + r.Target)
	}
	if r.Count >= 0 {
		fmt.Fprintf(&b, "\n%s%d", label("approval.field.count"), r.Count)
	}
	for _, s := range r.Samples {
		b.WriteString("\n  - " + approvalSampleLine(lang, s))
	}
	if r.Reversible {
		b.WriteString("\n" + uilang.Text(lang, "approval.reversible.yes"))
	} else {
		b.WriteString("\n" + uilang.Text(lang, "approval.reversible.no"))
	}
	return b.String()
}

func approvalAccountLabel(r core.ApprovalRequest) string {
	switch {
	case r.AccountName != "" && r.Account != "" && !strings.EqualFold(r.AccountName, r.Account):
		return r.AccountName + " (" + r.Account + ")"
	case r.AccountName != "":
		return r.AccountName
	}
	return r.Account
}

func approvalSampleLine(lang string, s core.ApprovalSample) string {
	parts := []string{}
	if s.Date != "" {
		parts = append(parts, s.Date)
	}
	if s.From != "" {
		parts = append(parts, s.From)
	}
	subject := s.Subject
	if subject == "" {
		subject = uilang.Text(lang, "approval.no_subject")
	}
	parts = append(parts, "\""+subject+"\"")
	return strings.Join(parts, " · ")
}
