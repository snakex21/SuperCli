package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"supercli/internal/llm"
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

// statusPhrases are whole status questions (after fillers and a few neutral
// tail words are removed). The match is exact on purpose: a status phrase
// followed by anything else ("co tam jest w pliku config.go?", "status:
// przetestuj build", "jak idzie? i zbadaj błąd") is a normal message, and the
// guard lets it through. When unsure, the user's message is never blocked.
var statusPhrases = map[string]bool{
	"jak idzie": true, "jak to idzie": true, "jak tam idzie": true, "jak tam": true, "co tam": true,
	"jak leci": true, "co słychać": true, "co slychac": true, "jak postępy": true, "jak postepy": true,
	"jakie postępy": true, "jakie postepy": true, "postęp": true, "postep": true, "postępy": true, "postepy": true,
	"status": true, "jaki status": true, "jaki jest status": true, "jaki stan": true, "jaki jest stan": true,
	"na jakim etapie": true, "na jakim etapie jesteśmy": true, "gdzie jesteśmy": true, "gdzie jestesmy": true,
	"ile zostało": true, "ile zostalo": true, "co już zrobione": true, "co juz zrobione": true, "co już zrobiłeś": true,
	"how is it going": true, "hows it going": true, "how are things": true, "how is the work going": true,
	"how far along": true, "whats the status": true, "what is the status": true, "any update": true,
	"any updates": true, "any progress": true, "progress": true, "where are we": true, "status update": true,
}

// statusTail words may close a status question without adding a request
// ("jak idzie z tym?", "how is it going so far?").
var statusTail = map[string]bool{
	"z": true, "tym": true, "pracą": true, "praca": true, "agentami": true, "workerami": true, "teraz": true,
	"u": true, "ciebie": true, "now": true, "so": true, "far": true, "with": true, "it": true, "this": true,
}

// smallTalk is a greeting or thanks with nothing to do ("hej", "dzięki").
// Like a status question it never starts or resumes workers. "ok" and "tak"
// are not here: they often approve a plan the coordinator just proposed.
var smallTalk = map[string]bool{
	"cześć": true, "czesc": true, "witam": true, "dzień dobry": true, "dzien dobry": true, "dobry wieczór": true,
	"dzięki": true, "dzieki": true, "dziękuję": true, "dziekuje": true, "dzięki wielkie": true, "wielkie dzięki": true,
	"hello": true, "thanks": true, "thank you": true, "thx": true, "good morning": true,
}

var greetingWords = map[string]bool{"hej": true, "hejka": true, "siema": true, "halo": true, "hi": true, "hey": true, "yo": true}

// isSmallTalk reports a greeting or thanks only.
func isSmallTalk(prompt string) bool {
	words := statusWords(prompt)
	if len(words) == 0 {
		return false
	}
	rest := words
	for len(rest) > 0 && statusFillers[rest[0]] {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		// Only greetings ("hej", "siema"); "no", "tak", "ok" may approve a plan.
		for _, w := range words {
			if !greetingWords[w] {
				return false
			}
		}
		return true
	}
	return smallTalk[strings.Join(rest, " ")]
}

// isStatusQuestion reports whether the whole message only asks how the work
// is going. Anything more (an instruction, a file, a second clause) and it is
// a normal message.
func isStatusQuestion(prompt string) bool {
	words := statusWords(prompt)
	for len(words) > 0 && statusFillers[words[0]] {
		words = words[1:]
	}
	for len(words) > 1 && statusTail[words[len(words)-1]] {
		if statusPhrases[strings.Join(words, " ")] {
			break
		}
		words = words[:len(words)-1]
	}
	return len(words) > 0 && statusPhrases[strings.Join(words, " ")]
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

// Worker continuation guard (1.0.39). Whether a worker that failed, lost its
// connection, was interrupted or was stopped should run again is the user's
// call, and so is stopping a running one:
//
//   - in an automatic turn (structural: no user message at all) it is always
//     refused, and so is a new task that repeats a worker of this
//     conversation (see AgentTool.repeatedBrief);
//   - in a user turn it needs an unambiguous signal: the Resume/Stop button
//     (whose message names the worker) or a message that names the worker
//     ("worker-7", "worker 7") in a clause without a negation. No keyword
//     guessing: "nie wznawiaj go", "tak, ale co to za plik?" or "ile to 7
//     plików?" are not a go-ahead.
//
// Deliberate decision: SuperCli keeps no record of an approved plan (plan
// mode is a prompt-only instruction; the goal tool tracks progress, not
// approval), so no plan can authorize an automatic resume. Only the user can.
func (l *Loop) wakeResumeRefusal(tc llm.ToolCall) string {
	var args struct {
		To     string `json:"to"`
		Mode   string `json:"mode"`
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		return ""
	}
	var target, action string
	switch tc.Name {
	case "send_message":
		if strings.TrimSpace(args.Mode) == "steer" {
			return "" // a correction for a running worker, not a restart
		}
		target, action = strings.TrimSpace(args.To), "resume"
	case "task_stop":
		target = strings.TrimSpace(args.ID)
		if target == "" {
			target = strings.TrimSpace(args.TaskID)
		}
		action = "stop"
	default:
		return ""
	}
	if target == "" {
		return ""
	}
	if action == "resume" {
		switch l.workerStatusOf(target) {
		case "failed", "stopped", WorkerStatusInterrupted:
		default:
			return "" // continuing a finished worker is ordinary delegation
		}
	}
	if l.wakeTurnForRun {
		return fmt.Sprintf("refused: this is an automatic turn without a user message, and %s %s is the user's decision. "+
			"Report what happened and offer it; the user uses the %s button or names the worker in a message.", action, target, map[string]string{"resume": "Resume", "stop": "Stop"}[action])
	}
	// No turn context (direct dispatch outside a run): nothing to judge by.
	if strings.TrimSpace(l.userPromptForRun) == "" || namesWorkerWithoutNegation(l.userPromptForRun, target, action) {
		return ""
	}
	return fmt.Sprintf("refused: to %s %s the user must ask for it explicitly (name the worker, or use the %s button); "+
		"the current message does not. Answer the user and offer it.", action, target, map[string]string{"resume": "Resume", "stop": "Stop"}[action])
}

// workerStatusOf finds a worker's state in the live registry, then in the
// database (a worker saved before a restart is not in memory).
func (l *Loop) workerStatusOf(id string) string {
	if l.workerStatusSource != nil {
		for _, s := range l.workerStatusSource() {
			if s.ID == id {
				return s.Status
			}
		}
	}
	if l.workerStatusLookup != nil {
		return l.workerStatusLookup(context.Background(), id)
	}
	return ""
}

var negationWords = map[string]bool{
	"nie": true, "nigdy": true, "bez": true, "not": true, "dont": true, "never": true, "nor": true,
	"cannot": true, "cant": true, "wont": true, "stop": true, "zatrzymaj": true, "przestań": true, "przestan": true,
}

// stopWords ask to stop; they negate a resume but are the request itself
// for a stop.
var stopWords = map[string]bool{"stop": true, "zatrzymaj": true, "przestań": true, "przestan": true}

// namesWorkerWithoutNegation: some clause of prompt names the worker ("worker-7",
// "worker 7", "workera 7", "agent 7") and has no negation word.
func namesWorkerWithoutNegation(prompt, workerID, action string) bool {
	num := strings.TrimPrefix(workerID, "worker-")
	if num == workerID || num == "" {
		return false
	}
	for _, clause := range splitClauses(prompt) {
		words := statusWords(clause)
		named := false
		for i, w := range words {
			if w == "worker"+num {
				named = true
			}
			if (w == "worker" || w == "workera" || w == "workerowi" || w == "agent" || w == "agenta") && i+1 < len(words) && words[i+1] == num {
				named = true
			}
		}
		if !named {
			continue
		}
		negated := false
		for _, w := range words {
			if negationWords[w] && !(action == "stop" && stopWords[w]) {
				negated = true
				break
			}
		}
		if !negated {
			return true
		}
	}
	return false
}

// splitClauses splits at sentence punctuation and contrast words ("ale", "but").
func splitClauses(text string) []string {
	text = strings.NewReplacer("'", "", "’", "", "-", "").Replace(strings.ToLower(text))
	parts := strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(".,;:!?\n", r) })
	var out []string
	for _, part := range parts {
		for _, sub := range strings.Split(" "+part+" ", " ale ") {
			out = append(out, strings.Split(sub, " but ")...)
		}
	}
	return out
}

// EmptyReplyNotice is shown when the model ended a turn without any text for
// the user (empty, or reasoning only). Hosts localize it by this exact text.
const EmptyReplyNotice = "empty reply: the model returned no answer; ask again or choose another model"
