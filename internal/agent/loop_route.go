package agent

import (
	"context"
	"strings"

	"supercli/internal/llm"
)

// prepareRunRoute decides the turn's route and attaches one-shot addons that
// only apply on the coordinator path (repo preflight, verification hint).
// Called once at the start of run(), after the user message is already on
// Messages. Routing uses the raw prompt; the session store keeps that raw
// prompt — addons are absent from the raw transcript.
func (l *Loop) prepareRunRoute(ctx context.Context, prompt string) {
	l.implementationHintForRun = ""
	l.screenshotForRun = containsScreenshotReference(prompt)
	l.headlessForRun = containsHeadlessReference(prompt)
	l.downloadForRun = l.downloadRequestForPrompt(prompt).download
	l.downloadPageForRun = l.downloadForRun && needsDownloadPageContract(prompt)
	webOnly := isSelfContainedWebRequest(prompt)
	if !webOnly && l.downloadForRun && !hasLocalWebRequestWork(unquotedActionPrompt(strings.ToLower(prompt)), true) {
		webOnly = l.previousDownloadWasWeb()
	}
	// A supplied attachment may carry project or local evidence omitted from
	// its caption. Preserve normal coordinator collection in that case.
	if len(l.Messages) > 0 && l.Messages[len(l.Messages)-1].HasImage() {
		webOnly = false
	}
	// A1: the navigator is a full LLM call. It runs here, inside the
	// background goroutine, so Run() returns immediately and the TUI
	// never blocks on Enter waiting for the route decision.
	switch {
	case l.screenshotForRun || l.headlessForRun || l.downloadForRun || webOnly:
		// Capture is an OS tool capability, not a conceptual chat question.
		l.route = RouteCoordinator
	case !l.navigate:
		// Navigator off: everything is coordinator (safe default for
		// scripted/worker use, which must keep the full tool context).
		l.route = RouteCoordinator
	case len(l.Messages) > 0 && l.Messages[len(l.Messages)-1].HasImage():
		// A new attachment needs the main model and project capabilities. Do not
		// upload it to a classifier first or route a caption-only greeting to chat.
		l.route = RouteCoordinator
	case l.navAuto:
		// Auto: take the cheap keyword decision on obvious turns and
		// only pay for the navigator model on ambiguous ones. Saves a
		// full model round-trip per confident turn.
		if mode, confident := l.routeMap.ClassifyConfident(prompt); confident {
			l.route = mode
		} else if l.navKeywordsOnly {
			l.route = l.routeMap.Classify(prompt)
		} else {
			l.route = l.navigateRoute(ctx, prompt)
		}
	default:
		l.route = l.navigateRoute(ctx, prompt)
	}
	// Repository context is useful only on the full coordinator route. Keep it
	// queued across chat/advisor turns and attach it to the newest user message
	// immediately before the first coordinator provider call. Routing above saw
	// only the user's raw prompt, and the session store keeps that raw prompt.
	if l.route == RouteCoordinator && !webOnly {
		l.attachCoordinatorAddon(ctx)
	}
	if l.route == RouteCoordinator && l.wordDocumentContext(prompt) {
		// These are registered but normally invisible. Promote them before the
		// first provider call so a Word request never spends a round trip on
		// tool_search and never sees scripting as the only usable route.
		l.registry.Activate("read_docx", "edit_docx")
	}
	// Keep optional guidance in the request-only tail. Mutating each user
	// message retained an identical copy for every editing turn, including
	// resumed workers. Earlier user text and the cacheable prefix stay intact.
	if l.route == RouteCoordinator && !l.skipImplementationHint {
		l.implementationHintForRun = implementationVerificationHint(prompt)
	}
}

func containsScreenshotReference(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, marker := range []string{"screenshot", "screen capture", "screen grab", "zrzut ekranu", "zrzut pulpitu", "zrzut z ekranu", "zrzutu ekranu", "zrzutu pulpitu", "zrzut okna", "zrzutu okna", "zrzut aplikacji", "zrzutu aplikacji", "window capture"} {
		if strings.Contains(p, marker) {
			return true
		}
	}
	return false
}

// attachCoordinatorAddon keeps collection off light turns and uses the current
// Run's context. Empty successful results are consumed; canceled work can retry
// on the next project turn. Both eager and lazy addons use the variable side.
func (l *Loop) attachCoordinatorAddon(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	block := l.nextCoordinatorAddon
	if source := l.nextCoordinatorAddonSource; source != nil {
		block = strings.TrimSpace(source(ctx))
	}
	if ctx.Err() != nil {
		return
	}
	l.nextCoordinatorAddon = ""
	l.nextCoordinatorAddonSource = nil
	if block != "" {
		l.appendCurrentUserContext(block)
	}
}

// The provider uses Parts instead of Content for multimodal messages. Put
// one-shot context in that same representation, copying the slice so a saved
// user message or an earlier request cannot acquire these additions.
func (l *Loop) appendCurrentUserContext(text string) {
	for i := len(l.Messages) - 1; i >= 0; i-- {
		msg := &l.Messages[i]
		if !isConversationUserTurn(*msg) {
			continue
		}
		*msg = llm.AppendContextText(*msg, llm.SourceRuntime, text)
		l.recordContextAddition(llm.Message{Role: llm.RoleSystem, Content: "\n\n" + text, Origin: &llm.ContextOrigin{Source: llm.SourceRuntime}})
		l.invalidateVisibleEstimate()
		return
	}
}

func (l *Loop) wordDocumentContext(prompt string) bool {
	if containsWordDocumentReference(prompt) {
		return true
	}
	// Web sessions construct a fresh Loop for each request, so carry the signal
	// from recent user text or prior native tool calls such as "popraw jeszcze".
	start := len(l.Messages) - 12
	if start < 0 {
		start = 0
	}
	for _, message := range l.Messages[start:] {
		if isConversationUserTurn(message) && containsWordDocumentReference(message.TextOnly().Content) {
			return true
		}
		if message.Role == llm.RoleAssistant {
			for _, call := range message.ToolCalls {
				if call.Name == "read_docx" || call.Name == "edit_docx" {
					return true
				}
			}
		}
	}
	return false
}

func containsWordDocumentReference(s string) bool {
	s = " " + strings.ToLower(strings.TrimSpace(s)) + " "
	for _, marker := range []string{".docx", " word ", " worda", " wordzie", " dokumencie word", " dokument word"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// continueWithDiscoveredTools restores project instructions and normal tools
// after a lightweight turn discovers a capability. This uses the already
// required tool-result continuation; no router call or repeated user prompt.
func (l *Loop) continueWithDiscoveredTools(ctx context.Context, calls []llm.ToolCall, outcomes []callOutcome) {
	if l.route == RouteCoordinator || len(l.registry.DiscoveredNames()) == 0 {
		return
	}
	for i, call := range calls {
		if call.Name != "tool_search" || i >= len(outcomes) || outcomes[i].failed {
			continue
		}
		l.route = RouteCoordinator
		l.attachCoordinatorAddon(ctx)
		return
	}
}

// Explicit endpoint requests receive the exact optional contract for this run.
func containsHeadlessReference(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, marker := range []string{"headless", "qemu", "qmp", "webdriver", "maszyną wirtualną", "maszyne wirtualna", "maszynę wirtualną", "maszyny wirtualnej", "maszynie wirtualnej"} {
		if strings.Contains(p, marker) {
			return true
		}
	}
	return false
}
