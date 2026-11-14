package headless

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"supercli/internal/tools/core"
)

func validAction(protocol, action string) (mutation bool, valid bool) {
	if protocol == "qmp" {
		switch action {
		case "status", "screenshot", "wait_event":
			return false, true
		case "keys", "type", "click":
			return true, true
		}
	} else if protocol == "webdriver" {
		switch action {
		case "status", "inspect", "screenshot":
			return false, true
		case "open", "navigate", "click", "type", "close":
			return true, true
		}
	}
	return false, false
}

// approval classifies one prepared call for the host approval gate. Invalid
// or out-of-scope calls fail here, before any prompt; read actions need no
// approval. It performs no I/O beyond the constructor's config snapshot.
func (t *Tool) approval(_ context.Context, raw json.RawMessage) (*core.ApprovalRequest, error) {
	p, _, err := normalize(raw)
	if err != nil {
		return nil, err
	}
	targetName, mutation, err := t.checkScope(p)
	if err != nil {
		return nil, fmt.Errorf("headless_control: %w", err)
	}
	if !mutation {
		return nil, nil
	}
	details, _ := json.MarshalIndent(p, "", "  ")
	effect := "This changes the selected VM/browser session."
	summaryKey := "approval.summary.headless_action"
	switch p.Action {
	case "open":
		effect = "This starts a headless browser session through the selected driver, creates its profile under the portable data directory, and may navigate to the requested URL. An empty binary uses the driver's default browser executable."
		summaryKey = "approval.summary.headless_open"
	case "close":
		effect = "This closes the specified browser session and removes its SuperCli-owned profile if one exists. It does not stop the driver."
		summaryKey = "approval.summary.headless_close"
	}
	target := fmt.Sprintf("%s (%s %s)", targetName, p.Protocol, p.Endpoint)
	if p.SessionID != "" {
		target += ", session " + p.SessionID
	}
	return &core.ApprovalRequest{
		Tool:        "headless_control",
		Operation:   p.Action,
		Risk:        core.RiskExternal,
		Reversible:  false,
		Target:      target,
		Destination: p.URL,
		Summary: fmt.Sprintf("Control trusted target %q (%s)? %s\nPortable data directory: %s\nWorkspace directory (base for a relative binary): %s\nComplete effective arguments:\n%s",
			targetName, p.Action, effect, t.DataDir, t.BaseDir, details),
		SummaryKey:    summaryKey,
		SummaryArgs:   map[string]string{"target": targetName, "action": p.Action},
		SummaryDetail: string(details),
		// Each mutation of a live VM or browser session is approved on its own.
		NoSessionGrant: true,
		Scope:          []string{"headless_control", p.Action, p.Protocol, targetName},
		Binding:        paramsBinding(p),
	}, nil
}

func paramsBinding(p params) string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// authorize checks the trusted scope before dialing, target locking or
// profile creation. A mutating action additionally needs the host approval
// for exactly these normalized arguments.
func (t *Tool) authorize(ctx context.Context, p params) error {
	_, mutation, err := t.checkScope(p)
	if err != nil {
		return err
	}
	if !mutation {
		return ctx.Err()
	}
	approved, ok := core.ApprovedRequest(ctx)
	if !ok || approved.Tool != "headless_control" || approved.Binding != paramsBinding(p) {
		return fmt.Errorf("action %q was not approved: it needs the user's approval in the SuperCli GUI or TUI; nothing was done", p.Action)
	}
	return ctx.Err()
}

// checkScope matches the call against the constructor's snapshot of the
// portable global [headless.targets].
func (t *Tool) checkScope(p params) (targetName string, mutation bool, err error) {
	mutation, valid := validAction(p.Protocol, p.Action)
	if !valid {
		return "", false, fmt.Errorf("unsupported %s action %q", p.Protocol, p.Action)
	}
	if t.scopeErr != nil {
		return "", false, fmt.Errorf("cannot load trusted headless configuration: %w", t.scopeErr)
	}
	names := make([]string, 0, len(t.scope.Targets))
	for name := range t.scope.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := t.scope.Targets[name]
		if target.Protocol != p.Protocol {
			continue
		}
		scheme := "tcp"
		if p.Protocol == "webdriver" {
			scheme = "http"
		}
		endpoint, err := localEndpoint(target.Endpoint, scheme)
		if err != nil {
			return "", false, fmt.Errorf("invalid configured headless target %q: %w", name, err)
		}
		if endpoint.String() != p.Endpoint {
			continue
		}
		if allowsAction(target.AllowedActions, p) {
			return name, mutation, nil
		}
	}
	return "", false, fmt.Errorf("endpoint/action is not allowed by portable global [headless.targets]; configure an exact protocol, endpoint and allowed_actions")
}

// QMP type sends the same keystrokes as keys, so a keys grant covers it.
// screenshot_after additionally needs the screenshot grant.
func allowsAction(allowed []string, p params) bool {
	has := func(action string) bool { return slices.Contains(allowed, action) }
	ok := has(p.Action) || p.Protocol == "qmp" && p.Action == "type" && has("keys")
	return ok && (!p.ScreenshotAfter || has("screenshot"))
}
