package agent

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
			"plik", "pliki", "folder", "projekt", "projekcie", "repo", "kod", "funkcj", "test", "build", "błąd", "blad",
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
	if utf8.RuneCountInString(p) <= 80 {
		// A greeting can introduce an already-recognized social message.
		// Unknown continuations still fall through to project routing.
		p = withoutChatOpening(p, m.ChatExact)
		for _, exact := range m.ChatExact {
			if p == exact {
				return RouteChatOnly, true
			}
		}
		if undecidedChat(p) {
			return RouteChatOnly, true
		}
		for _, prefix := range m.ChatPrefixes {
			if strings.HasPrefix(p, prefix) {
				return RouteChatOnly, true
			}
		}
	}
	if utf8.RuneCountInString(p) <= 240 {
		for _, prefix := range m.AdvisorPrefixes {
			if strings.HasPrefix(p, prefix) {
				return RouteAdvisor, true
			}
		}
	}
	return RouteCoordinator, false
}

// withoutChatOpening removes a complete greeting or acknowledgement, never
// a word fragment (e.g. "hi" in "history"). Coordinator keywords are checked
// against the original message before this helper is used.
func withoutChatOpening(p string, openings []string) string {
	for _, opening := range openings {
		if !strings.HasPrefix(p, opening) || len(p) == len(opening) {
			continue
		}
		rest := p[len(opening):]
		first, _ := utf8.DecodeRuneInString(rest)
		if unicode.IsSpace(first) || unicode.IsPunct(first) {
			return strings.TrimLeftFunc(rest, func(r rune) bool {
				return unicode.IsSpace(r) || unicode.IsPunct(r)
			})
		}
	}
	return p
}

const navigatorSystemPrompt = `You are SuperCli's navigator. Choose which map the next user message should use.

Return only compact JSON: {"mode":"chat|advisor|coordinator|clarify","reason":"..."}

Modes:
- chat: obvious small talk, greetings, short social replies.
- advisor: conceptual advice or explanation that does not require inspecting this machine/project/files.
- coordinator: requires project/files/code/terminal/tools, or asks what is here/in this repo, or asks to build/test/edit/fix.
- clarify: the user asks an ambiguous question and there is not enough context to choose advisor or coordinator.

Do not use keyword matching blindly. Read the recent context and infer intent. Prefer coordinator when project-specific evidence is needed. Prefer advisor when general reasoning is enough.`

const chatOnlySystemPrompt = `You are SuperCli. Answer directly in the user's language. For plain conversation, reply briefly without tools. Use web_lookup for current facts, recall for remembered facts, and tool_search to obtain any other tools needed to fulfill the request.`

const advisorSystemPrompt = `You are SuperCli. Give thoughtful advice in the user's language. Use web_lookup for current facts, recall for remembered facts, and tool_search when the answer requires other tools or project evidence. Only claim to have inspected what you actually checked.`

const implementationVerificationInstruction = `Requested edits only: implement and verify unless blocked; honor read-only requests. Each tool turn is another provider request: batch independent reads/searches, use read_many for multiple files/ranges, combine related search_code terms with regex alternation, and send several independent read-only tool calls together. Once evidence suffices, make requested edits instead of exploring one file per round. If a tool call fails, correct it or report the blocker. After changing code, run the most relevant build/test/check and, when practical, exercise the changed program. Do not claim completion before a concrete check succeeds; report exactly what passed and what was not verified.`

// implementationVerificationHint recognizes explicit mutation intent without
// a model call. Keep the list narrow: asking about a project must not inherit a
// testing lecture, while concrete implementation work should.
func implementationVerificationHint(prompt string) string {
	p := unquotedActionPrompt(strings.ToLower(prompt))
	for _, hit := range []string{
		"napraw", "zaimplement", "dodaj", "usuń", "usun", "zmień", "zmien", "edytuj",
		"przerób", "przerob", "popraw", "zbuduj", "stwórz", "stworz", "zrób", "zrob",
		"implement", "fix ", "fix:", "add ", "remove ", "change ", "edit ", "refactor", "build ", "rebuild ", "reimplement",
	} {
		if hasUnnegatedActionWord(p, hit) {
			return implementationVerificationInstruction
		}
	}
	return ""
}

// hasUnnegatedActionWord guards the cheap hint classifier, not routing or tool
// permissions. Stems still recognize Polish inflections, but embedded fragments
// (prefix, credit, identifiers), negations and read-only mentions are not edit requests.
func hasUnnegatedActionWord(prompt, fragment string) bool {
	if fragment == "" {
		return false
	}
	for offset := 0; offset < len(prompt); {
		relative := strings.Index(prompt[offset:], fragment)
		if relative < 0 {
			return false
		}
		at := offset + relative
		offset = at + len(fragment)
		if at > 0 {
			previous, _ := utf8.DecodeLastRuneInString(prompt[:at])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) || unicode.IsMark(previous) || previous == '_' {
				continue
			}
		}
		if actionMentionContext(prompt[:at]) {
			continue
		}
		return true
	}
	return false
}

// These conservative guards only omit optional guidance. They neither authorize
// edits nor restrict tools; ambiguous intent remains with the model. In
// particular, a quoted action or an explanation of an action is not a request
// to perform it.
func unquotedActionPrompt(prompt string) string {
	runes := []rune(prompt)
	var closing rune
	for i, r := range runes {
		if closing != 0 {
			runes[i] = ' '
			if r == closing {
				closing = 0
			}
			continue
		}
		switch r {
		case '"', '`', '“', '„', '«', '‘', '\'':
			// Apostrophes inside words are contractions, not quotations.
			if (r == '\'' || r == '‘') && i > 0 && unicode.IsLetter(runes[i-1]) {
				continue
			}
			closing = r
			if r == '“' || r == '„' {
				closing = '”'
			} else if r == '«' {
				closing = '»'
			} else if r == '‘' {
				closing = '’'
			}
			runes[i] = ' '
		}
	}
	return string(runes)
}

func actionMentionContext(before string) bool {
	// Keep sentence boundaries: "I think not. Fix it" is a positive request.
	clause := before[strings.LastIndexAny(before, ".!?;\n")+1:]
	words := strings.Fields(before)
	for i := len(words) - 1; i >= 0; i-- {
		switch words[i] {
		case "actually", "really", "ever", "directly", "just", "należy", "nalezy":
			continue
		case "not", "never", "don't", "don’t", "cannot", "can't", "can’t", "nie", "bez":
			return true
		}
		break
	}
	// Explicit sequencing can introduce implementation after an explanation.
	for _, transition := range []string{" then ", " potem ", " następnie ", " nastepnie "} {
		if at := strings.LastIndex(clause, transition); at >= 0 {
			clause = clause[at+len(transition):]
		}
	}
	clause = strings.TrimLeft(clause, " \t,(")
	clause = strings.TrimPrefix(clause, "please ")
	for _, prefix := range []string{
		"explain ", "describe ", "review ", "tell me ", "show me ", "how ", "whether ",
		"wyjaśnij ", "wyjasnij ", "wytłumacz ", "wytlumacz ", "opisz ", "jak ",
	} {
		if strings.HasPrefix(clause, prefix) {
			// "Review and fix" requests an edit; "explain how to fix and
			// refactor" still only requests an explanation.
			if strings.HasSuffix(strings.TrimSpace(clause), " and") &&
				!strings.Contains(clause, "how ") && !strings.Contains(clause, "whether ") {
				return false
			}
			return true
		}
	}
	return false
}

// undecidedChat accepts only complete short statements of indecision. A
// prefix such as "nie wiem" alone is not enough: any unrecognized remainder
// (e.g. "sprawdź" or "kontynuuj") leaves the coordinator available.
func undecidedChat(p string) bool {
	p = strings.Join(strings.FieldsFunc(p, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",.!?…", r)
	}), " ")
	clauses := [...]string{
		"nie wiem", "zastanawiam się", "zastanawiam sie", "się zastanawiam", "sie zastanawiam",
		"na razie", "narazie", "jeszcze", "właśnie", "wlasnie",
	}
	hasStatement := false
	for p != "" {
		matched := false
		for i, clause := range clauses {
			if p == clause || strings.HasPrefix(p, clause+" ") {
				hasStatement = hasStatement || i < 5
				p = strings.TrimSpace(strings.TrimPrefix(p, clause))
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return hasStatement
}
