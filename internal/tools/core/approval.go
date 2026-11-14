package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// RiskLevel classifies the effect of one tool operation. Tools declare it per
// operation through Tool.Approval; the host, not the model, decides whether an
// operation runs.
type RiskLevel string

const (
	// RiskRead never changes user data or external state.
	RiskRead RiskLevel = "read"
	// RiskWriteReversible changes user data in a way the user can undo
	// (copy, move, create a folder, move to Trash).
	RiskWriteReversible RiskLevel = "write_reversible"
	// RiskDestructive can lose data (permanent delete, empty Trash).
	RiskDestructive RiskLevel = "destructive"
	// RiskExternal has an effect outside this machine that SuperCli cannot
	// undo (for example uploading a message to a mail server).
	RiskExternal RiskLevel = "external_side_effect"
)

// ApprovalSample is one message shown to the user in an approval request:
// headers only, never a body.
type ApprovalSample struct {
	From    string `json:"from,omitempty"`
	Subject string `json:"subject,omitempty"`
	Date    string `json:"date,omitempty"`
}

// ApprovalPreview holds details that cost I/O to collect. The approver asks for
// it only when a human will actually see the request.
type ApprovalPreview struct {
	AccountName string           `json:"account_name,omitempty"`
	Count       int              `json:"count"` // -1 = unknown
	Samples     []ApprovalSample `json:"samples,omitempty"`
	// Summary, when set, replaces ApprovalRequest.Summary: the preview can
	// name things the cheap classifier could not (an account display name
	// instead of "account2").
	Summary string `json:"summary,omitempty"`
	// Warnings are short sentences the user should read before deciding,
	// for example "message 42 is not in folder INBOX". Trusted tool code
	// writes them from read-only lookups; they never contain model text
	// beyond identifiers the request already shows.
	Warnings []string `json:"warnings,omitempty"`
}

// ApprovalRequest describes one risky operation in human terms. It is built by
// trusted tool code from the host-prepared arguments.
type ApprovalRequest struct {
	Tool        string    `json:"tool"`
	Operation   string    `json:"operation"`
	Risk        RiskLevel `json:"risk"`
	Reversible  bool      `json:"reversible"`
	Account     string    `json:"account,omitempty"`
	Folder      string    `json:"folder,omitempty"`
	Destination string    `json:"destination,omitempty"`
	// Target names the object of a non-folder operation (a file path, a
	// contact, an address book).
	Target string `json:"target,omitempty"`
	// Summary is one plain English sentence describing the effect.
	Summary string `json:"summary"`
	// SummaryKey optionally names a UI catalogue message that localizes
	// Summary; SummaryArgs fills its {name} placeholders. Both are set by
	// trusted tool code only. Summary stays the English fallback.
	SummaryKey  string            `json:"summary_key,omitempty"`
	SummaryArgs map[string]string `json:"summary_args,omitempty"`
	// SummaryDetail is untranslated data (for example the exact effective
	// arguments) shown after the localized summary. Summary already ends
	// with it.
	SummaryDetail string `json:"summary_detail,omitempty"`
	// Scope identifies "similar" operations: tool, operation, account, folder
	// and (for transfers) destination. Session grants and the config allowlist
	// match it segment by segment.
	Scope []string `json:"scope"`
	// PreApproved skips the prompt for an operation the user already approved,
	// for example the continuation of an approved batch. It must only be set
	// from host-side state, never from model arguments.
	PreApproved string `json:"-"`
	// NoSessionGrant hides "approve similar" for irreversible operations.
	NoSessionGrant bool `json:"no_session_grant,omitempty"`
	// Binding is opaque host state the approval covers (for example a hash of
	// the exact effective arguments or of the approved goal). Fn reads it back
	// with ApprovedRequest and refuses to run when the state changed between
	// the prompt and execution. It is never shown or logged.
	Binding string `json:"-"`
	// Preview is optional and called at most once, only before a human prompt.
	Preview func(context.Context) ApprovalPreview `json:"-"`
	// Filled by the approver from Preview.
	AccountName string           `json:"account_name,omitempty"`
	Count       int              `json:"count"`
	Samples     []ApprovalSample `json:"samples,omitempty"`
	Warnings    []string         `json:"warnings,omitempty"`
}

// ApplyPreview copies a preview into the request the user will see.
func (r *ApprovalRequest) ApplyPreview(p ApprovalPreview) {
	r.AccountName, r.Count, r.Samples, r.Warnings = p.AccountName, p.Count, p.Samples, p.Warnings
	if strings.TrimSpace(p.Summary) != "" {
		r.Summary = p.Summary
	}
}

// ScopeKey is the canonical "tool:op:account:folder[:destination]" string.
func (r ApprovalRequest) ScopeKey() string {
	parts := make([]string, 0, len(r.Scope))
	for _, part := range r.Scope {
		parts = append(parts, NormalizeApprovalScopePart(part))
	}
	return strings.Join(parts, ":")
}

// NormalizeApprovalScopePart lowercases and trims one scope segment and keeps
// ':' out of it so a segment never spans two positions.
func NormalizeApprovalScopePart(part string) string {
	part = strings.ToLower(strings.TrimSpace(part))
	return strings.ReplaceAll(part, ":", "_")
}

// Approval modes recorded in ApprovalDecision.Mode and the audit log.
const (
	ApprovalOnce         = "once"
	ApprovalSimilar      = "session"
	ApprovalSessionGrant = "session_grant"
	ApprovalAllowlist    = "allowlist"
	ApprovalAllowAll     = "allow_all"
	ApprovalPreApproved  = "pre_approved"
	ApprovalDenied       = "denied"
	ApprovalUnavailable  = "no_ui"
)

// ApprovalDecision is the host's verdict for one request.
// ApprovalReasonUserDenied is the decision reason when the user explicitly
// declined the prompt (Deny button, n/Esc in the TUI, closing the card).
const ApprovalReasonUserDenied = "the user denied it"

type ApprovalDecision struct {
	Approved bool
	Mode     string
	Reason   string
}

// Approver decides whether a risky operation may run. Implementations ask the
// user in an interactive UI or apply the non-interactive policy.
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) ApprovalDecision
}

// ErrApprovalRequired marks a tool error caused by a missing or refused user
// approval. Nothing was executed; the model must not retry on its own.
var ErrApprovalRequired = errors.New("user approval required")

type approverKey struct{}
type approvalSessionKey struct{}
type approvalGrantKey struct{}

type approverBox struct{ a Approver }

var defaultApprover atomic.Pointer[approverBox]

// SetDefaultApprover installs the process approver (TUI, GUI or batch policy).
// nil restores the fail-closed default.
func SetDefaultApprover(a Approver) {
	if a == nil {
		defaultApprover.Store(nil)
		return
	}
	defaultApprover.Store(&approverBox{a: a})
}

// WithApprover overrides the approver for one run (tests, embedded hosts).
func WithApprover(ctx context.Context, a Approver) context.Context {
	return context.WithValue(ctx, approverKey{}, a)
}

// ApproverFrom returns the context approver, then the process default, then a
// fail-closed approver that denies everything.
func ApproverFrom(ctx context.Context) Approver {
	if a, ok := ctx.Value(approverKey{}).(Approver); ok && a != nil {
		return a
	}
	if box := defaultApprover.Load(); box != nil && box.a != nil {
		return box.a
	}
	return denyApprover{}
}

// WithApprovalSession scopes "approve similar in this session" grants.
func WithApprovalSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, approvalSessionKey{}, strings.TrimSpace(sessionID))
}

// ApprovalSession returns the session id set by WithApprovalSession.
func ApprovalSession(ctx context.Context) string {
	s, _ := ctx.Value(approvalSessionKey{}).(string)
	return s
}

type approvalGrant struct {
	decision ApprovalDecision
	request  ApprovalRequest
}

// ApprovalGranted reports the decision under which the current tool call runs.
// Tools use it to remember host state (for example continuation tokens) that
// only an approved call may create.
func ApprovalGranted(ctx context.Context) (ApprovalDecision, bool) {
	g, ok := ctx.Value(approvalGrantKey{}).(approvalGrant)
	return g.decision, ok && g.decision.Approved
}

// ApprovedRequest returns the request the host approved for the current tool
// call. Tools whose Fn must not run without approval check it (and its
// Binding) instead of trusting that Fn is only reached through the registry.
func ApprovedRequest(ctx context.Context) (ApprovalRequest, bool) {
	g, ok := ctx.Value(approvalGrantKey{}).(approvalGrant)
	if !ok || !g.decision.Approved {
		return ApprovalRequest{}, false
	}
	return g.request, true
}

type denyApprover struct{}

func (denyApprover) Approve(context.Context, ApprovalRequest) ApprovalDecision {
	return ApprovalDecision{Mode: ApprovalUnavailable, Reason: "no approval UI or policy is configured"}
}

// ApprovalDeniedError is the tool error the model sees for a refused request.
// It names SuperCli's approval prompt as the source: the wording "NOT
// executed" made models report that the tool itself (for example Thunderbird)
// had refused, although the user had declined the card.
func ApprovalDeniedError(req ApprovalRequest, d ApprovalDecision) error {
	what := req.Tool
	if req.Operation != "" {
		what += " " + req.Operation
	}
	if d.Mode == ApprovalUnavailable {
		return fmt.Errorf("%w: %s was NOT executed. It changes user data and needs the user's explicit approval, but no interactive approval UI is available in this run (%s). Nothing was changed. Do not retry and do not set confirm yourself; tell the user what you wanted to do. They can run it in the SuperCli GUI/TUI, or allow it for non-interactive runs with [approvals] auto_approve = [%q] in config.toml", ErrApprovalRequired, what, d.Reason, req.ScopeKey())
	}
	reason := strings.TrimSpace(d.Reason)
	if reason == "" || reason == ApprovalReasonUserDenied {
		return fmt.Errorf("%w: the user declined this action (%s) in the SuperCli approval prompt; nothing was done. The %s tool did not refuse or fail: it was never called. Don't retry it and don't try another way to do the same thing; tell the user it was not done because they declined it, and ask how to proceed", ErrApprovalRequired, what, req.Tool)
	}
	return fmt.Errorf("%w: this action (%s) was not approved in the SuperCli approval prompt because %s; nothing was done. The %s tool did not refuse or fail: it was never called. Don't retry it on your own; tell the user it was not done and ask how to proceed", ErrApprovalRequired, what, reason, req.Tool)
}

// gateInvocation applies Tool.Approval. It returns the arguments to execute and
// the execution context, or a refusal result.
func gateInvocation(ctx context.Context, t Tool, args json.RawMessage) (json.RawMessage, context.Context, *Result) {
	if t.Approval == nil {
		return args, ctx, nil
	}
	// The host owns the confirmation argument: whatever the model sent is
	// discarded before the risk is assessed.
	if t.ApprovalArg != "" {
		var ok bool
		if args, ok = deleteJSONField(args, t.ApprovalArg); !ok {
			// Fail closed: arguments the host cannot parse could still carry a
			// model-supplied confirmation.
			return nil, ctx, &Result{Err: fmt.Errorf("%w: %s arguments are not a JSON object; nothing was executed", ErrInvalidToolArgs, t.Name)}
		}
	}
	req, err := t.Approval(ctx, args)
	if err != nil {
		return nil, ctx, &Result{Err: err}
	}
	if req == nil {
		return args, ctx, nil
	}
	if req.Tool == "" {
		req.Tool = t.Name
	}
	var decision ApprovalDecision
	if req.PreApproved != "" {
		decision = ApprovalDecision{Approved: true, Mode: ApprovalPreApproved, Reason: req.PreApproved}
		if a, ok := ApproverFrom(ctx).(interface {
			Record(context.Context, ApprovalRequest, ApprovalDecision)
		}); ok {
			a.Record(ctx, *req, decision)
		}
	} else {
		decision = ApproverFrom(ctx).Approve(ctx, *req)
	}
	if !decision.Approved {
		return nil, ctx, &Result{Err: SelfContainedErr(ApprovalDeniedError(*req, decision))}
	}
	if t.ApprovalArg != "" {
		args = setJSONTrue(args, t.ApprovalArg)
	}
	return args, context.WithValue(ctx, approvalGrantKey{}, approvalGrant{decision: decision, request: *req}), nil
}

// deleteJSONField removes field from a JSON object. ok is false when args is
// not empty and not a JSON object, so the caller can refuse the call.
func deleteJSONField(args json.RawMessage, field string) (json.RawMessage, bool) {
	if len(bytes.TrimSpace(args)) == 0 {
		return args, true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil || m == nil {
		return args, false
	}
	if _, ok := m[field]; !ok {
		return args, true
	}
	delete(m, field)
	out, err := json.Marshal(m)
	if err != nil {
		return args, false
	}
	return out, true
}

func setJSONTrue(args json.RawMessage, field string) json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(args)) > 0 && json.Unmarshal(args, &m) != nil {
		return args
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	m[field] = json.RawMessage("true")
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}
