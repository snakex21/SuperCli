package agent

import (
	"fmt"
	"strings"
	"unicode"
)

// Resuming workers in a user turn (after 1.0.40, simplified). Phrase rules
// kept blocking real requests: "możesz je wznowić ..., nie?" read as a
// question, a model-not-found failure read as a task failure, so five resumes
// were refused while the user watched. The user is present in a user turn and
// sees what the coordinator does, so:
//
//   - in a user turn (a typed message or a button) the coordinator may resume
//     any worker the message plausibly refers to - no imperative or question
//     matching, no failure-type gate;
//   - an explicit negation about resuming stops it ("nie wznawiaj", "don't
//     resume", "zostaw je"), and workers named in a negated clause are left
//     out ("wznów je, ale nie 376");
//   - worker numbers in the message narrow it ("wznów 363");
//   - a worker the user stopped must be named;
//   - automatic and wake turns never resume (wakeResumeRefusal).

// resumeIntent is what a user message says about resuming workers.
type resumeIntent struct {
	// negated: the message says not to resume ("nie wznawiaj", "zostaw je").
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

// resumeNegators directly before a resume verb negate it ("nie wznawiaj",
// "don't resume", "never continue"); "nie?" elsewhere in the sentence does not.
var resumeNegators = map[string]bool{"nie": true, "not": true, "dont": true, "never": true, "nigdy": true, "no": true, "nicht": true, "ne": true, "не": true}

// leaveWords followed by a pronoun or "workers" mean "leave them stopped".
var leaveWords = map[string]bool{"zostaw": true, "zostawcie": true, "leave": true, "lass": true, "laisse": true, "deja": true, "лиши": true, "залиш": true, "оставь": true}

var leaveObjects = map[string]bool{"je": true, "ich": true, "go": true, "them": true, "it": true, "workery": true, "agentów": true, "agentow": true, "workerów": true, "workerow": true, "sie": true, "les": true, "los": true, "их": true}

func readResumeIntent(prompt string) resumeIntent {
	in := resumeIntent{named: map[string]bool{}, excluded: map[string]bool{}}
	for _, c := range splitClauses(prompt) {
		words := statusWords(c.text)
		negatedResume, negatedClause := false, false
		for i, w := range words {
			if negationWords[w] {
				negatedClause = true
			}
			if isResumeWord(w) && ((i > 0 && resumeNegators[words[i-1]]) || (i > 1 && resumeNegators[words[i-2]])) {
				negatedResume = true
			}
			if leaveWords[w] && i+1 < len(words) && leaveObjects[words[i+1]] {
				negatedResume = true
			}
		}
		in.negated = in.negated || negatedResume
		for _, num := range workerNumbers(words) {
			// A number in a clause with a negation ("ale nie 376") is left
			// out; "200 GB, nie?" names no worker and is ignored by callers.
			if negatedClause && !c.question {
				in.excluded[num] = true
			} else {
				in.named[num] = true
			}
		}
	}
	return in
}

// resumeRefusal decides a resume of a failed, stopped or interrupted worker
// in a user turn: "" allows it, otherwise a reason the coordinator relays.
// known holds this conversation's worker numbers (a bare number names one
// only when such a worker exists).
func resumeRefusal(prompt, target, status string, known map[string]bool) string {
	num := strings.TrimPrefix(target, "worker-")
	in := readResumeIntent(prompt)
	if in.negated {
		return fmt.Sprintf("refused: the user said not to resume workers now. Do not resume %s.", target)
	}
	if in.excluded[num] && known[num] {
		return fmt.Sprintf("refused: the user said not to resume %s. Do not resume it.", target)
	}
	named := map[string]bool{}
	for n := range in.named {
		if known[n] || n == num {
			named[n] = true
		}
	}
	if len(named) > 0 && !named[num] {
		return fmt.Sprintf("refused: the user asked to resume only the workers they named, not %s.", target)
	}
	if status == "stopped" && !named[num] {
		return fmt.Sprintf("refused: %s was stopped on request, so a general resume does not include it. "+
			"Resume the others; ask the user in one short sentence whether to resume %s too.", target, target)
	}
	return ""
}
