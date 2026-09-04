package agent

import (
	"fmt"
	"strings"
	"time"
)

// chatRouteTools is the minimal tool set sent on the chat/advisor
// routes: just enough for the model to pull in more (tool_search can
// activate web_search etc.) and to remember the user (recall). The
// full tool list is deliberately NOT loaded outside the coordinator
// route — that is the token-saving point of the router.
var chatRouteTools = []string{"web_lookup", "tool_search", "recall"}

// thinCoreTools is the small, always-full-schema core for the thin
// tool protocol (B2b). On the coordinator route, when thin tools are
// enabled, ONLY these tools carry their full JSON Schema every turn;
// every other tool is advertised by name+hint in a compact catalog
// and pulled in on demand via tool_search (which activates it and
// returns its full schema in the same turn). tool_search itself MUST
// be here — it is the gateway to the rest.
// thinCoreTools is the default full-schema set on the coordinator route
// when thin tools are enabled. Keep this small: one discovery path, one
// edit path (patch_file), one create path. The line editors that used to
// sit beside them are gone; write_file stays registered for whole-file
// rewrites but is NOT core — models must not need tool_search to edit
// ordinary files.
var thinCoreTools = []string{
	"tool_search",
	"web_lookup",
	"invoke_tool",
	"read_output",
	"patch_file",
	"create_file",
	"read_lines",
	"read_many",
	"read_image",
	"load_session_image",
	"search_code",
	"ctx_execute",
	"ask_user",
	"recall",
	// list_dir is the "what's in this folder?" primitive. It must
	// be schema-carrying core, not dormant tail.
	"list_dir",
}

// isThinCore reports whether name is in the thin-core set.
func isThinCore(name string) bool {
	for _, n := range thinCoreTools {
		if n == name {
			return true
		}
	}
	return false
}

// timeSection returns the per-request freshness stamp injected into
// the system prompt of every route. Rebuilt on each provider call so
// the model always knows the current date, time, and timezone, plus
// an explicit nudge to verify currency of advice. Costs a few tokens.
func timeSection(now time.Time) string {
	return fmt.Sprintf("Current local date/time: %s. It is %d — verify that libraries, APIs, and patterns you recommend are still current before advising.",
		now.Format("Monday, 2006-01-02 15:04 MST (UTC-07:00)"), now.Year())
}

// RouteMode is the pre-request context mode selected by the routing map.
type RouteMode string

const (
	RouteCoordinator RouteMode = "coordinator"
	RouteChatOnly    RouteMode = "chat"
	RouteAdvisor     RouteMode = "advisor"
	RouteClarify     RouteMode = "clarify"
)

// RouteMap is the lightweight map that keeps obvious casual conversation out
// of the full agent/coordinator context. It is intentionally data-shaped (not a
// model behavior hidden in weights): later it can be loaded from
// .supercli/router.toml without changing the agent loop.
type RouteMap struct {
	ChatExact       []string
	ChatPrefixes    []string
	AdvisorPrefixes []string
	CoordinatorHits []string
}

func DefaultRouteMap() RouteMap {
	return RouteMap{
		ChatExact: []string{
			"cześć", "czesc", "hej", "siema", "elo", "witam", "hello", "hi", "hey",
			"ok", "okej", "dobra", "dobrze", "a dobrze", "spoko", "dzięki", "dzieki", "thanks",
		},
		ChatPrefixes: []string{
			"co tam", "jak tam", "lubisz", "kim jesteś", "kim jestes", "opowiedz żart", "opowiedz zart",
			"przetłumacz", "przetlumacz", "translate",
		},
		// These prefixes describe self-contained conceptual questions. The
		// coordinator keyword pass runs first, so "wyjaśnij ten kod/plik"
		// still gets project tools while "wyjaśnij rekurencję" avoids a
		// navigator model call and uses the lightweight advisor route.
		AdvisorPrefixes: []string{
			"wyjaśnij", "wyjasnij", "wytłumacz", "wytlumacz", "co to jest", "co oznacza", "co znaczy",
			"jak działa", "jak dziala", "dlaczego", "jaka jest różnica", "jaka jest roznica",
			"explain", "what is", "what does", "how does", "why does", "difference between",
		},
		CoordinatorHits: []string{
			"plik", "pliki", "folder", "projekt", "repo", "kod", "funkcj", "test", "build", "błąd", "blad",
			"napraw", "zrób", "zrob", "dodaj", "usuń", "usun", "zmień", "zmien", "edytuj",
			"uruchom", "komenda", "terminal", "powershell", "cmd", "go test", "go build",
			"docx", "xlsx", "pdf", "zip", "screenshot", "tutaj", "tu jest", "co tutaj", "co jest w",
		},
	}
}

func (m RouteMap) Classify(prompt string) RouteMode {
	mode, _ := m.ClassifyConfident(prompt)
	return mode
}

// ClassifyConfident is Classify plus a confidence signal. confident is
// true only when the prompt matched an explicit rule (a coordinator
// keyword, or a chat exact/prefix); it is false when the classifier fell
// through to its RouteCoordinator default (empty or ambiguous input).
// The navigator's auto mode uses this to take the cheap keyword decision
// on obvious turns and reserve the extra model round-trip for genuinely
// ambiguous ones (advisor vs coordinator), which keywords cannot judge.
func (m RouteMap) ClassifyConfident(prompt string) (mode RouteMode, confident bool) {
	p := strings.ToLower(strings.TrimSpace(prompt))
	p = strings.Trim(p, " \t\r\n.!?…")
	if p == "" {
		return RouteCoordinator, false
	}
	for _, hit := range m.CoordinatorHits {
		if strings.Contains(p, hit) {
			return RouteCoordinator, true
		}
	}
	for _, exact := range m.ChatExact {
		if p == exact {
			return RouteChatOnly, true
		}
	}
	if len([]rune(p)) <= 80 {
		for _, prefix := range m.ChatPrefixes {
			if strings.HasPrefix(p, prefix) {
				return RouteChatOnly, true
			}
		}
	}
	if len([]rune(p)) <= 240 {
		for _, prefix := range m.AdvisorPrefixes {
			if strings.HasPrefix(p, prefix) {
				return RouteAdvisor, true
			}
		}
	}
	return RouteCoordinator, false
}

const navigatorSystemPrompt = `You are SuperCli's navigator. Choose which map the next user message should use.

Return only compact JSON: {"mode":"chat|advisor|coordinator|clarify","reason":"..."}

Modes:
- chat: obvious small talk, greetings, short social replies.
- advisor: conceptual advice or explanation that does not require inspecting this machine/project/files.
- coordinator: requires project/files/code/terminal/tools, or asks what is here/in this repo, or asks to build/test/edit/fix.
- clarify: the user asks an ambiguous question and there is not enough context to choose advisor or coordinator.

Do not use keyword matching blindly. Read the recent context and infer intent. Prefer coordinator when project-specific evidence is needed. Prefer advisor when general reasoning is enough.`

const chatOnlySystemPrompt = `You are SuperCli in chat-only mode. Answer directly and briefly in the user's language. Use web_lookup to verify current facts, recall for remembered user facts, and tool_search only to find other capabilities. Do not use tools for plain conversation. If the user asks for project/file/code/terminal/document work, ask them to repeat the request naming the file or repo, or to set navigator = "off" in config.toml.`

const advisorSystemPrompt = `You are SuperCli in advisor mode. Give thoughtful conceptual advice in the user's language, but do not claim to have inspected files, code, terminal output, or project state. Use web_lookup for current facts, recall for remembered facts, and tool_search for other capabilities. If project-specific evidence is needed, tell the user to repeat the request naming the file or repo, or to set navigator = "off" in config.toml.`

const implementationVerificationInstruction = `Completion contract: continue past discovery and make the requested change unless blocked. Each assistant tool turn is another provider request: batch independent reads/searches, use read_many for multiple files/ranges, combine related search_code terms with regex alternation, and send several independent read-only tool calls together. Once evidence is sufficient, edit instead of exploring one file per round. If a tool call fails, correct it or report the blocker. After changing code, run the most relevant build/test/check and, when practical, exercise the changed program. Do not claim completion before a concrete check succeeds; report exactly what passed and what was not verified.`

// implementationVerificationHint recognizes explicit mutation intent without
// a model call. Keep the list narrow: asking about a project must not inherit a
// testing lecture, while concrete implementation work should.
func implementationVerificationHint(prompt string) string {
	p := strings.ToLower(prompt)
	for _, hit := range []string{
		"napraw", "zaimplement", "dodaj", "usuń", "usun", "zmień", "zmien", "edytuj",
		"przerób", "przerob", "popraw", "zbuduj", "stwórz", "stworz", "zrób", "zrob",
		"implement", "fix ", "fix:", "add ", "remove ", "change ", "edit ", "refactor", "build ",
	} {
		if strings.Contains(p, hit) {
			return implementationVerificationInstruction
		}
	}
	return ""
}
