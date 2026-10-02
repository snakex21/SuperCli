package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"supercli/internal/tools/interactive"
)

func validAction(protocol, action string) (mutation bool, valid bool) {
	if protocol == "qmp" {
		switch action {
		case "status", "screenshot", "wait_event":
			return false, true
		case "keys", "click":
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

// Scope and approval are checked before dialing, target locking or profile
// creation. The constructor owns an independent global-config snapshot.
func (t *Tool) authorize(ctx context.Context, p params) error {
	mutation, valid := validAction(p.Protocol, p.Action)
	if !valid {
		return fmt.Errorf("unsupported %s action %q", p.Protocol, p.Action)
	}
	if t.scopeErr != nil {
		return fmt.Errorf("cannot load trusted headless configuration: %w", t.scopeErr)
	}
	names := make([]string, 0, len(t.scope.Targets))
	for name := range t.scope.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	targetName := ""
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
			return fmt.Errorf("invalid configured headless target %q: %w", name, err)
		}
		if endpoint.String() != p.Endpoint {
			continue
		}
		for _, action := range target.AllowedActions {
			if action == p.Action {
				targetName = name
				break
			}
		}
		if targetName != "" {
			break
		}
	}
	if targetName == "" {
		return fmt.Errorf("endpoint/action is not allowed by portable global [headless.targets]; configure an exact protocol, endpoint and allowed_actions")
	}
	if !mutation {
		return ctx.Err()
	}
	details, _ := json.MarshalIndent(p, "", "  ")
	effect := "This changes the selected VM/browser session."
	if p.Action == "open" {
		effect = "This starts a headless browser session through the selected driver, creates its profile under the portable data directory, and may navigate to the requested URL. An empty binary uses the driver's default browser executable."
	}
	if p.Action == "close" {
		effect = "This closes the specified browser session and removes its SuperCli-owned profile if one exists. It does not stop the driver."
	}
	question := fmt.Sprintf("Control trusted target %q?\n%s\nPortable data directory: %s\nWorkspace directory (base for a relative binary): %s\nComplete effective arguments:\n%s", targetName, effect, t.DataDir, t.BaseDir, details)
	return interactive.ConfirmAction(ctx, t.confirmation, question)
}
