package agent

import "strings"

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
			"wyjaśnij mi", "wyjasnij mi", "co znaczy", "przetłumacz", "przetlumacz",
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
	p := strings.ToLower(strings.TrimSpace(prompt))
	p = strings.Trim(p, " \t\r\n.!?…")
	if p == "" {
		return RouteCoordinator
	}
	for _, hit := range m.CoordinatorHits {
		if strings.Contains(p, hit) {
			return RouteCoordinator
		}
	}
	for _, exact := range m.ChatExact {
		if p == exact {
			return RouteChatOnly
		}
	}
	if len([]rune(p)) <= 80 {
		for _, prefix := range m.ChatPrefixes {
			if strings.HasPrefix(p, prefix) {
				return RouteChatOnly
			}
		}
	}
	return RouteCoordinator
}

const navigatorSystemPrompt = `You are SuperCli's navigator. Choose which map the next user message should use.

Return only compact JSON: {"mode":"chat|advisor|coordinator|clarify","reason":"..."}

Modes:
- chat: obvious small talk, greetings, short social replies.
- advisor: conceptual advice or explanation that does not require inspecting this machine/project/files.
- coordinator: requires project/files/code/terminal/tools, or asks what is here/in this repo, or asks to build/test/edit/fix.
- clarify: the user asks an ambiguous question and there is not enough context to choose advisor or coordinator.

Do not use keyword matching blindly. Read the recent context and infer intent. Prefer coordinator when project-specific evidence is needed. Prefer advisor when general reasoning is enough.`

const chatOnlySystemPrompt = `You are SuperCli in chat-only mode. Answer directly and briefly in the user's language. No tools are available in this mode. If the user asks for project/file/code/terminal/document work, say you need agent mode and ask them to repeat or clarify the task.`

const advisorSystemPrompt = `You are SuperCli in advisor mode. Give thoughtful conceptual advice in the user's language, but do not claim to have inspected files, code, terminal output, or project state. If project-specific evidence is needed, say that agent/coordinator mode should inspect it.`
