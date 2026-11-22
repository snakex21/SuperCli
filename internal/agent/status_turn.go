package agent

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Status turns (1.0.39). A coordinator with workers that is asked only how
// the work is going ("i jak idzie?", "status?", "how's it going") must answer
// from what it already knows: the workers' states and their reports in the
// conversation. Before this, such a message went down the normal coordinator
// path, and a model seeing a failed worker ("Context retained; send_message
// ... continues this worker") resumed it instead of answering. The user then
// watched a new worker run they never asked for, and the coordinator looked
// busy for as long as that synchronous run lasted.
//
// The guard is enforced at dispatch, not only by asking the model: in a
// status turn task, send_message and task_stop are refused with a reason the
// model can relay, so no worker is started, resumed, steered or stopped.

// statusTurnHint is request-only (never persisted). The worker list is added
// after it by statusTurnContext.
const statusTurnHint = "[status question — not a work request] The user only asked how the work is going. " +
	"Answer now, in the user's language, from the worker states below and the latest worker reports in this conversation: " +
	"what finished, what is still running, what failed or was stopped and why, and what remains. " +
	"Do not start, resume, retry, steer or stop workers in this turn and do not begin new work; " +
	"if something should be resumed or started, say so and wait for the user's instruction."

// statusTurnRefusal is the tool result of a delegation call in a status turn.
const statusTurnRefusal = "refused: the user only asked for a status update, so this turn must not start, resume, steer or stop workers. " +
	"Answer with the current status from the worker states and reports; propose next steps and wait for the user's instruction."

// statusTurnTools are the calls that would change what workers do.
var statusTurnTools = map[string]bool{"task": true, "send_message": true, "task_stop": true}

// statusTurnMaxWorkers bounds the worker list in the hint (most recent first).
const statusTurnMaxWorkers = 12

// statusFillers may precede the question without changing its meaning
// ("i jak idzie?", "ok, status?", "so how's it going").
var statusFillers = map[string]bool{
	"i": true, "a": true, "no": true, "to": true, "hej": true, "hejka": true, "siema": true, "halo": true,
	"ok": true, "okej": true, "dobra": true, "więc": true, "wiec": true, "and": true, "so": true,
	"well": true, "hi": true, "hey": true, "yo": true, "tak": true, "oraz": true,
}

// statusPhrases start a status question (after fillers are removed).
var statusPhrases = []string{
	"jak idzie", "jak to idzie", "jak tam idzie", "jak tam", "co tam", "jak leci", "co słychać", "co slychac",
	"jak postępy", "jak postepy", "jakie postępy", "jakie postepy", "postęp", "postep", "postępy", "postepy",
	"status", "jaki status", "jaki jest status", "jaki stan", "jaki jest stan", "na jakim etapie",
	"gdzie jesteśmy", "gdzie jestesmy", "ile zostało", "ile zostalo", "co już zrobione", "co juz zrobione",
	"how is it going", "hows it going", "how are things", "how is the work going", "how far along",
	"whats the status", "what is the status", "any update", "any updates", "any progress", "progress", "update",
	"where are we",
}

// statusActionStems mark a request for work, which is never a status-only turn
// ("co tam, kontynuuj", "status and resume worker-3").
var statusActionStems = []string{
	"kontynu", "wznów", "wznow", "dalej", "odpal", "uruchom", "puść", "pusc", "zrób", "zrob", "napraw", "dodaj",
	"zacznij", "rozpocznij", "dokończ", "dokoncz", "popraw", "zmień", "zmien", "usuń", "usun", "zatrzymaj", "stop",
	"continue", "resume", "retry", "restart", "start", "run", "fix", "keep", "finish", "implement", "add",
}

// statusMaxWords keeps the classifier to short questions; a longer message
// carries more than a status request and takes the normal path.
const statusMaxWords = 8

// isStatusQuestion reports whether prompt only asks how the work is going.
func isStatusQuestion(prompt string) bool {
	words := statusWords(prompt)
	for len(words) > 0 && statusFillers[words[0]] {
		words = words[1:]
	}
	if len(words) == 0 || len(words) > statusMaxWords {
		return false
	}
	for _, w := range words {
		for _, stem := range statusActionStems {
			if strings.HasPrefix(w, stem) {
				return false
			}
		}
	}
	text := strings.Join(words, " ")
	for _, phrase := range statusPhrases {
		if text == phrase {
			return true
		}
		// One-word nouns that double as verbs or objects ("update the
		// readme", "postęp w testach") count only on their own.
		if statusExactOnly[phrase] {
			continue
		}
		if strings.HasPrefix(text, phrase+" ") {
			return true
		}
	}
	return false
}

var statusExactOnly = map[string]bool{
	"update": true, "progress": true, "postęp": true, "postep": true, "postępy": true, "postepy": true,
}

// statusWords lowercases prompt, drops apostrophes ("how's" -> "hows") and
// splits on everything that is not a letter or digit.
func statusWords(prompt string) []string {
	p := strings.ToLower(strings.TrimSpace(prompt))
	p = strings.NewReplacer("'", "", "’", "").Replace(p)
	return strings.FieldsFunc(p, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// hasDelegationTools reports whether this loop can start or resume workers.
func (l *Loop) hasDelegationTools() bool {
	if l == nil || l.registry == nil {
		return false
	}
	for name := range statusTurnTools {
		if _, ok := l.registry.Get(name); ok {
			return true
		}
	}
	return false
}

// statusTurnContext is the request-only block of a status turn: the hint and
// the conversation's workers from the live registry.
func (l *Loop) statusTurnContext() string {
	var snaps []Snapshot
	if l.workerStatusSource != nil {
		snaps = l.workerStatusSource()
	}
	return statusTurnHint + "\n" + renderStatusWorkers(snaps)
}

func renderStatusWorkers(snaps []Snapshot) string {
	if len(snaps) == 0 {
		return "Workers of this conversation: none in the live registry; use the worker reports in this conversation."
	}
	snaps = append([]Snapshot(nil), snaps...)
	sort.SliceStable(snaps, func(i, j int) bool { return snaps[i].UpdatedAt.After(snaps[j].UpdatedAt) })
	counts := map[string]int{}
	for _, s := range snaps {
		counts[s.Status]++
	}
	states := make([]string, 0, len(counts))
	for status, n := range counts {
		states = append(states, fmt.Sprintf("%s %d", status, n))
	}
	sort.Strings(states)
	var b strings.Builder
	fmt.Fprintf(&b, "Workers of this conversation (%s; most recent first):", strings.Join(states, ", "))
	for i, s := range snaps {
		if i == statusTurnMaxWorkers {
			fmt.Fprintf(&b, "\n- … %d older worker(s)", len(snaps)-i)
			break
		}
		fmt.Fprintf(&b, "\n- %s (%s) %s, runs %d", s.ID, s.Agent, s.Status, s.Runs)
		model := strings.TrimSpace(s.Model)
		if model == "" {
			model = strings.TrimSpace(s.WorkerModel)
		}
		if model != "" {
			fmt.Fprintf(&b, ", model %s", model)
		}
		if e := strings.TrimSpace(s.LastError); e != "" {
			fmt.Fprintf(&b, ", last error: %s", statusClip(e, 160))
		}
		if d := strings.TrimSpace(s.Description); d != "" {
			fmt.Fprintf(&b, " — %s", statusClip(d, 120))
		}
	}
	return b.String()
}

func statusClip(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
