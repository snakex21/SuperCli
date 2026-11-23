package agent

import (
	"fmt"
	"strings"
	"unicode"
)

// Resuming workers in a user turn (after 1.0.39). The earlier rule asked for
// each worker by name, so "dobra, możesz wznowić te modele" after a usage
// limit was refused five times and the user had to retype the worker ids:
// a click simulator, like the questions the command guard no longer asks.
// Now the user's plain request decides:
//
//   - a resume imperative without a negation or a question ("wznów je",
//     "możesz wznowić te modele", "kontynuuj workery", "resume them") covers
//     every worker that stopped on a rate or usage limit, a connection or
//     server error, or a restart (interrupted);
//   - worker numbers in the request ("wznów 363", "worker-363, 376") narrow
//     it to those workers, and a named worker runs again whatever stopped it;
//   - a worker the user stopped, or one whose task genuinely failed, is not
//     included in a general request: the coordinator asks about it;
//   - a worker stopped by a rate or usage limit needs no special words: any
//     user message lets the coordinator continue it, unless the message says
//     not to ("nie wznawiaj");
//   - automatic turns (no user message) never resume anything.

// resumeIntent is what a user message says about resuming workers.
type resumeIntent struct {
	// request: a clause plainly asks to resume/continue (imperative or "you
	// can resume"), without negation, condition or question.
	request bool
	// negated: a clause says not to resume ("nie wznawiaj ich").
	negated bool
	// named: worker numbers in clauses without a negation; excluded: numbers
	// in clauses with one ("wznów 363, ale nie 376").
	named, excluded map[string]bool
}

// resumeWords are resume/continue verbs in the languages of the UI; a word
// also counts when it starts with one of resumeStems (inflected and clitic
// forms: "wznowić", "reanúdalos", "продолжай").
var resumeWords = map[string]bool{
	"wznów": true, "wznow": true, "wznawiaj": true, "wznówcie": true, "wznowcie": true, "kontynuuj": true, "kontynuujcie": true,
	"dokończ": true, "dokoncz": true, "dokończcie": true, "ponów": true, "ponow": true, "ponowić": true, "ponowic": true,
	"resume": true, "continue": true, "retry": true, "restart": true, "rerun": true, "unpause": true,
	"weiter": true, "weitermachen": true, "fortfahren": true, "fortsetzen": true, "setze": true,
	"continúa": true, "continua": true, "continúe": true, "continuar": true, "continuez": true, "continuer": true,
	"continuare": true, "riprendi": true, "reprends": true, "reprenez": true, "reprendre": true, "retome": true, "retomar": true,
	"pokračuj": true, "pokracuj": true, "pokračujte": true, "obnov": true, "obnovte": true, "folytasd": true, "folytatsd": true,
	"fortsæt": true, "fortsett": true, "fortsätt": true, "jatka": true, "jätka": true, "tęsk": true, "turpini": true, "devam": true,
}

var resumeStems = []string{
	"wznow", "wznów", "kontynu", "reanud", "riprend", "retom", "fortsetz", "wiederaufnehm", "hervat",
	"продолж", "возобнов", "продовж", "віднов", "възобнов", "продължи", "nastavi", "pokračuj", "folytat", "jatka", "συνέχισ",
	"sürdür", "devam", "turpin", "nadaljuj", "genoptag", "återuppt", "reia", "reluați", "continuă", "tęsk", "folytasd",
}

// resumeModals precede an infinitive in a polite request ("możesz wznowić",
// "can you resume them", "you can continue"); with them the verb need not
// open the clause.
var resumeModals = map[string]bool{
	"możesz": true, "mozesz": true, "możecie": true, "mozecie": true, "można": true, "mozna": true, "mógłbyś": true, "moglbys": true,
	"mogłabyś": true, "moglabys": true, "spokojnie": true, "już": true, "juz": true, "ponownie": true, "znowu": true, "tam": true,
	"can": true, "could": true, "would": true, "you": true, "go": true, "ahead": true, "just": true, "may": true, "again": true,
}

func isResumeWord(w string) bool {
	if resumeWords[w] {
		return true
	}
	for _, stem := range resumeStems {
		if strings.HasPrefix(w, stem) {
			return true
		}
	}
	return false
}

// workerNumbers returns the worker numbers a clause names: "worker363",
// "worker 363", "agenta 7" or a bare number.
func workerNumbers(words []string) []string {
	var out []string
	for _, w := range words {
		num := w
		for _, prefix := range []string{"workera", "workerowi", "worker", "agenta", "agent"} {
			if rest, ok := strings.CutPrefix(w, prefix); ok && rest != "" {
				num = rest
				break
			}
		}
		if num != "" && strings.IndexFunc(num, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
			out = append(out, num)
		}
	}
	return out
}

func readResumeIntent(prompt string) resumeIntent {
	in := resumeIntent{named: map[string]bool{}, excluded: map[string]bool{}}
	for _, c := range splitClauses(prompt) {
		words := statusWords(c.text)
		if len(words) == 0 {
			continue
		}
		negated := false
		for _, w := range words {
			if negationWords[w] {
				negated = true
				break
			}
		}
		for _, num := range workerNumbers(words) {
			if negated {
				in.excluded[num] = true
			} else if !c.question {
				in.named[num] = true
			}
		}
		verb := -1
		for i, w := range words {
			if isResumeWord(w) {
				verb = i
				break
			}
		}
		if verb < 0 {
			continue
		}
		// "czy możesz wznowić" / "can you resume" without "?" is a request;
		// any other question ("dlaczego nie wznowiłeś") is neither a request
		// nor a refusal.
		if c.question || questionWords[words[0]] && !resumeModals[words[0]] && !(len(words) > 1 && resumeModals[words[1]]) {
			continue
		}
		if negated {
			in.negated = true
			continue
		}
		if conditional(words) {
			continue
		}
		in.request = true
	}
	return in
}

// stopKind classifies why a failed worker stopped.
type stopKind int

const (
	stopTaskFailure stopKind = iota // the task itself failed: repeat only when named
	stopLimit                       // rate, usage or subscription limit
	stopTransient                   // connection, server or timeout error, restart
	stopByUser                      // stopped on request
)

var limitMarkers = []string{
	"429", "rate limit", "rate_limit", "ratelimit", "rate-limit", "rate limited", "usage limit", "usage_limit", "usage-limit",
	"too many requests", "quota", "limit reached", "limit exceeded", "subscription limit", "credit balance", "insufficient_quota",
	"out of credits", "billing",
}

var transientMarkers = []string{
	"connection", "timeout", "timed out", "deadline exceeded", "eof", "reset by peer", "broken pipe", "overloaded", "529",
	"500 ", "502", "503", "504", "server error", "service unavailable", "bad gateway", "unavailable", "stream", "network",
	"tls", "dial tcp", "no such host", "temporarily", "interrupted",
}

func classifyStop(status, lastError string) stopKind {
	switch status {
	case "stopped":
		return stopByUser
	case WorkerStatusInterrupted:
		return stopTransient
	}
	e := strings.ToLower(lastError)
	for _, m := range limitMarkers {
		if strings.Contains(e, m) {
			return stopLimit
		}
	}
	for _, m := range transientMarkers {
		if strings.Contains(e, m) {
			return stopTransient
		}
	}
	return stopTaskFailure
}

// resumeRefusal decides a resume of a failed, stopped or interrupted worker
// in a user turn: "" allows it, otherwise a reason the coordinator relays.
// known holds this conversation's worker numbers (a bare number names one
// only when such a worker exists).
func resumeRefusal(prompt, target, status, lastError string, known map[string]bool) string {
	num := strings.TrimPrefix(target, "worker-")
	in := readResumeIntent(prompt)
	if in.excluded[num] {
		return fmt.Sprintf("refused: the user said not to resume %s. Do not resume it.", target)
	}
	named := map[string]bool{}
	for n := range in.named {
		if known[n] || n == num {
			named[n] = true
		}
	}
	kind := classifyStop(status, lastError)
	if in.request {
		if named[num] {
			return ""
		}
		if len(named) > 0 {
			return fmt.Sprintf("refused: the user asked to resume only the workers they named, not %s.", target)
		}
		switch kind {
		case stopLimit, stopTransient:
			return ""
		case stopByUser:
			return fmt.Sprintf("refused: %s was stopped on request, so a general resume does not include it. "+
				"Resume the others; ask the user in one short sentence whether to resume %s too.", target, target)
		default:
			return fmt.Sprintf("refused: %s failed on its task (%s), so a general resume does not repeat it. "+
				"Resume the others; tell the user why it failed and ask whether to run it again.", target, statusClip(lastError, 160))
		}
	}
	if in.negated {
		return fmt.Sprintf("refused: the user said not to resume workers now. Do not resume %s.", target)
	}
	if kind == stopLimit {
		return "" // stopped by a usage limit: continuing it needs no special words
	}
	if namesWorkerWithoutNegation(prompt, target, "resume") {
		return ""
	}
	return fmt.Sprintf("refused: to resume %s the user must ask for it explicitly (ask to resume or continue, name the worker, or use the Resume button); "+
		"the current message does not. Answer the user and offer it.", target)
}
