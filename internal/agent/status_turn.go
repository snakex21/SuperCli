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
	"Keep it short: 2-4 lines (done / in progress / blocked), no option lists unless the user must decide something, " +
	"details only if the user asks. " +
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
		status := s.Status
		if strings.HasPrefix(s.Review, "unchecked:") && (status == "done" || status == "completed") {
			status = "done (file changes not checked: workspace too large)"
		} else if s.Review != "" && (status == "done" || status == "completed") {
			// The same state the panel shows: done, but needs a look.
			status = "done, needs review (" + strings.TrimPrefix(s.Review, "review: ") + ")"
		}
		fmt.Fprintf(&b, "\n- %s (%s) %s, runs %d", s.ID, s.Agent, status, s.Runs)
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
		if title := workerTaskTitle(s.Description); title != "" {
			// The task, not the shared project preamble most briefs open with.
			fmt.Fprintf(&b, " — %s", title)
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
//   - stopping in a user turn needs a message that names the worker
//     ("worker-7", "worker 7") in a clause without a negation, or the Stop
//     button;
//   - resuming in a user turn follows what the user plainly asked (see
//     resumeRefusal): "wznów je", "możesz wznowić te modele", "resume them"
//     cover every worker that stopped on a limit, a connection error or a
//     restart; named workers narrow it; "nie wznawiaj" stops it. Workers the
//     user stopped and genuine task failures run again only when named.
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
	var status string
	if action == "resume" {
		status, _ = l.workerStatusOf(target)
		switch status {
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
	if strings.TrimSpace(l.userPromptForRun) == "" {
		return ""
	}
	if action == "resume" {
		return resumeRefusal(l.userPromptForRun, target, status, l.conversationWorkerNumbers())
	}
	if namesWorkerWithoutNegation(l.userPromptForRun, target, action) {
		return ""
	}
	return fmt.Sprintf("refused: to %s %s the user must ask for it explicitly (name the worker, or use the %s button); "+
		"the current message does not. Answer the user and offer it.", action, target, map[string]string{"resume": "Resume", "stop": "Stop"}[action])
}

// workerStatusOf finds a worker's state and last error in the live registry,
// then in the database (a worker saved before a restart is not in memory).
func (l *Loop) workerStatusOf(id string) (string, string) {
	if l.workerStatusSource != nil {
		for _, s := range l.workerStatusSource() {
			if s.ID != id {
				continue
			}
			if s.LastError == "" && s.Status == "failed" && l.workerStatusLookup != nil {
				// Saved-only workers are listed without their error.
				if _, lastError := l.workerStatusLookup(context.Background(), id); lastError != "" {
					return s.Status, lastError
				}
			}
			return s.Status, s.LastError
		}
	}
	if l.workerStatusLookup != nil {
		return l.workerStatusLookup(context.Background(), id)
	}
	return "", ""
}

// conversationWorkerNumbers lists the numbers of this conversation's workers
// ("worker-363" -> "363"), so a bare number in a message can name one.
func (l *Loop) conversationWorkerNumbers() map[string]bool {
	out := map[string]bool{}
	if l.workerStatusSource == nil {
		return out
	}
	for _, s := range l.workerStatusSource() {
		if num := strings.TrimPrefix(s.ID, "worker-"); num != s.ID && num != "" {
			out[num] = true
		}
	}
	return out
}

// Explicit consent (review 2): the user asks for it in the imperative, in a
// clause that names the worker, is not a question and has no negation or
// "leave it / skip". Mentioning a worker ("dlaczego worker-7 padł?", "pokaż
// log workera 7", "zostaw worker-7 w spokoju") is not consent.
var resumeVerbs = map[string]bool{
	"wznów": true, "wznow": true, "wznawiaj": true, "kontynuuj": true, "dokończ": true, "dokoncz": true,
	"ponów": true, "ponow": true, "resume": true, "continue": true, "retry": true, "restart": true,
}

var stopVerbs = map[string]bool{
	"zatrzymaj": true, "przerwij": true, "stop": true, "kill": true, "anuluj": true, "cancel": true, "halt": true,
}

var negationWords = map[string]bool{
	"nie": true, "nigdy": true, "bez": true, "not": true, "dont": true, "never": true, "nor": true, "no": true,
	"cannot": true, "cant": true, "wont": true, "zostaw": true, "zostawić": true, "zostawic": true, "skip": true,
	"pomiń": true, "pomin": true, "leave": true, "ignore": true, "ignoruj": true, "nieważne": true, "niewazne": true,
}

// conditionWords make an order conditional ("continue worker-7 only once
// worker-3 finishes", "stop it if it fails"): not consent to act now.
var conditionWords = map[string]bool{
	"if": true, "when": true, "once": true, "after": true, "until": true, "unless": true, "later": true, "afterwards": true,
	"dopiero": true, "jeśli": true, "jesli": true, "jeżeli": true, "jezeli": true, "gdy": true, "kiedy": true, "później": true,
	"pozniej": true, "potem": true, "chyba": true, "ewentualnie": true, "wtedy": true,
}

// conditional reports a condition in the clause words; "jak" counts only in
// its "when it finishes" sense ("jak skończy", "jak tylko").
func conditional(words []string) bool {
	for i, w := range words {
		if conditionWords[w] {
			return true
		}
		if (w == "jak" || w == "as") && i+1 < len(words) {
			next := words[i+1]
			if next == "tylko" || next == "soon" || strings.HasPrefix(next, "skończ") || strings.HasPrefix(next, "skoncz") ||
				strings.HasPrefix(next, "zakończ") || strings.HasPrefix(next, "zakoncz") || strings.HasPrefix(next, "będzie") {
				return true
			}
		}
	}
	return false
}

// questionWords open a question; a question is never consent.
var questionWords = map[string]bool{
	"dlaczego": true, "czemu": true, "czy": true, "co": true, "jak": true, "kiedy": true, "gdzie": true, "ile": true,
	"why": true, "what": true, "how": true, "when": true, "where": true, "does": true, "did": true, "is": true,
	"was": true, "can": true, "could": true, "should": true,
}

// politeWords may precede the imperative verb ("proszę wznów", "please resume").
var politeWords = map[string]bool{"proszę": true, "prosze": true, "please": true, "pls": true, "ok": true, "okej": true, "dobra": true, "teraz": true, "now": true, "to": true, "i": true, "and": true, "a": true}

// namesWorkerWithoutNegation reports explicit consent to action ("resume" or
// "stop") on workerID in prompt.
func namesWorkerWithoutNegation(prompt, workerID, action string) bool {
	num := strings.TrimPrefix(workerID, "worker-")
	if num == workerID || num == "" {
		return false
	}
	verbs := resumeVerbs
	if action == "stop" {
		verbs = stopVerbs
	}
	for _, clause := range splitClauses(prompt) {
		if clause.question {
			continue
		}
		words := statusWords(clause.text)
		named := false
		for i, w := range words {
			if w == "worker"+num || (w == "worker" || w == "workera" || w == "workerowi" || w == "agent" || w == "agenta") && i+1 < len(words) && words[i+1] == num {
				named = true
			}
		}
		if !named || len(words) == 0 || questionWords[words[0]] {
			continue
		}
		negated := false
		for _, w := range words {
			if negationWords[w] {
				negated = true
				break
			}
		}
		if negated || conditional(words) {
			continue
		}
		// The imperative verb opens the clause (after polite words).
		i := 0
		for i < len(words) && politeWords[words[i]] {
			i++
		}
		if i < len(words) && verbs[words[i]] {
			// "stop worrying about worker-7", "przestań się martwić": the verb
			// governs another activity, not the worker.
			if i+1 < len(words) && (strings.HasSuffix(words[i+1], "ing") || words[i+1] == "się" || words[i+1] == "sie") {
				continue
			}
			return true
		}
	}
	return false
}

type clause struct {
	text     string
	question bool
}

// splitClauses splits at sentence punctuation, colons and contrast words
// ("ale", "but"); a clause ending in "?" is a question.
func splitClauses(text string) []clause {
	text = strings.NewReplacer("'", "", "’", "", "-", "").Replace(strings.ToLower(text))
	var out []clause
	start := 0
	for i, r := range text {
		if strings.ContainsRune(".,;:!?\n", r) {
			out = append(out, splitContrast(text[start:i], r == '?')...)
			start = i + len(string(r))
		}
	}
	out = append(out, splitContrast(text[start:], false)...)
	return out
}

func splitContrast(part string, question bool) []clause {
	var out []clause
	for _, sub := range strings.Split(" "+part+" ", " ale ") {
		for _, c := range strings.Split(sub, " but ") {
			if strings.TrimSpace(c) != "" {
				out = append(out, clause{text: c, question: question})
			}
		}
	}
	return out
}

// maxCutOffContinues bounds the automatic continuations of a reply cut by the
// output limit in one Run.
const maxCutOffContinues = 2

// EmptyReplyNotice is shown when the model ended a turn without any text for
// the user (empty, or reasoning only). Hosts localize it by this exact text.
const EmptyReplyNotice = "empty reply: the model returned no answer; ask again or choose another model"
