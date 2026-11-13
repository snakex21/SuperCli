package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"supercli/internal/tools"
)

// verificationTrail remembers, per Run, which commands ran and how the last
// check (test/build/lint run) ended. It is evidence for one non-blocking
// check: a final answer that claims passing checks when the last check
// failed, or when no command ran at all, gets a visible note. In the eval
// suite 3 of 5 failing runs ended with "Done ... verified".
type verificationTrail struct {
	mu         sync.Mutex
	commands   bool
	ran        bool
	lastFailed bool
	command    string
}

func (v *verificationTrail) reset() {
	v.mu.Lock()
	v.commands, v.ran, v.lastFailed, v.command = false, false, false, ""
	v.mu.Unlock()
}

// looseCheckRE recognises checks inside compound shell lines too
// ("cd x && go test ./..."), which the strict failed-check guard ignores.
var looseCheckRE = regexp.MustCompile(`(?i)\b(?:go\s+(?:test|vet|build)|pytest|unittest|node\s+--test|(?:npm|pnpm|yarn|bun)(?:\s+run)?\s+(?:test|build|lint|typecheck)|npx\s+(?:jest|vitest|tsc|eslint)|jest|vitest|cargo\s+(?:test|check|build|clippy)|dotnet\s+(?:test|build)|make\s+(?:test|check)|ctest|mvn\s+test|gradle\w*\s+test)\b`)

// observeResult records one finished command. Only commands that look like
// checks move the "last check" state.
func (v *verificationTrail) observeResult(command []string, result tools.Result) {
	if len(command) == 0 {
		return
	}
	line := strings.Join(command, " ")
	check := isVerificationCommand(command) || looseCheckRE.MatchString(line)
	failed := result.Err != nil
	if execution := result.Execution; execution != nil {
		failed = failed || !execution.Completed || execution.ExitCode != 0
	} else if !failed {
		var exit struct {
			Code *int `json:"exit_code"`
		}
		if json.Unmarshal([]byte(result.Text), &exit) == nil && exit.Code != nil {
			failed = *exit.Code != 0
		}
	}
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.commands = true
	if check {
		v.ran, v.lastFailed, v.command = true, failed, line
	}
}

// verificationClaimRE matches an answer stating that checks passed. English
// and Polish forms; deliberately narrow so ordinary prose does not trigger it.
var verificationClaimRE = regexp.MustCompile(`(?i)\b(?:(?:all\s+)?(?:the\s+)?(?:unit\s+)?(?:tests?|checks?|builds?|test suite)\s+(?:now\s+|still\s+|all\s+)?(?:pass(?:es|ed|ing)?|succeed(?:s|ed)?|(?:are|is)\s+(?:passing|green))|verified|verification\s+(?:passed|succeeded)|zweryfikowa\w*|(?:testy|testów)\s+(?:przechodz\w*|przeszły|zaliczon\w*)|przechodz\w*\s+(?:wszystkie\s+)?testy)\b`)

// A claim preceded by a negation in the same clause is not a claim.
var verificationNegationRE = regexp.MustCompile(`(?i)(?:\bnot|n't|\bnever|\bwithout|\bno|\bcannot|\bunable to|\bnie)\b[^.;:!?\n]{0,24}$`)

func claimsVerification(text string) bool {
	text = thinkingBlockRE.ReplaceAllString(text, "")
	for _, loc := range verificationClaimRE.FindAllStringIndex(text, -1) {
		before := text[max(0, loc[0]-40):loc[0]]
		if verificationNegationRE.MatchString(before) {
			continue
		}
		// "I verified this by running go test; each version fails" reports a
		// failure honestly.
		start := 0
		for _, b := range sentenceEndRE.FindAllStringIndex(text[:loc[0]], -1) {
			start = b[1]
		}
		end := len(text)
		if b := sentenceEndRE.FindStringIndex(text[loc[1]:]); b != nil {
			end = loc[1] + b[0]
		}
		if verificationFailureRE.MatchString(text[start:end]) {
			continue
		}
		// "I verified that the file does not exist" reports a finding, not a
		// passing check.
		word := strings.ToLower(text[loc[0]:loc[1]])
		if word == "verified" || strings.HasPrefix(word, "zweryfikowa") {
			if verificationFindingRE.MatchString(text[loc[1]:min(len(text), loc[1]+24)]) {
				continue
			}
		}
		return true
	}
	return false
}

var (
	thinkingBlockRE       = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)
	sentenceEndRE         = regexp.MustCompile(`[.!?](?:\s|$)|\n`)
	verificationFailureRE = regexp.MustCompile(`(?i)\bfail|\bconflict|\bcontradict|\berror|\bnie przech|\bbłęd`)
)

var verificationFindingRE = regexp.MustCompile(`(?i)^\s+(?:that|whether|if|the\s+(?:file|path|folder|directory)|it\b|there\b|że|czy)`)

// verificationClaimWarning returns a note to append to a final answer, or "".
// It never blocks the answer and never changes what the model said.
func (l *Loop) verificationClaimWarning(text string) string {
	if !claimsVerification(text) {
		return ""
	}
	l.verifyTrail.mu.Lock()
	commands, ran, failed, command := l.verifyTrail.commands, l.verifyTrail.ran, l.verifyTrail.lastFailed, l.verifyTrail.command
	l.verifyTrail.mu.Unlock()
	switch {
	case !commands:
		// Commands that are not recognisable checks (a CLI run, a script)
		// may well be the verification; only no command at all is flagged.
		return "\n\n[SuperCli note: this answer reports passing checks, but no command ran in this turn.]"
	case ran && failed:
		return fmt.Sprintf("\n\n[SuperCli note: this answer reports passing checks, but the last check in this turn failed: %s]", command)
	}
	return ""
}

// workerRunKey marks the context of a worker's own Run. Worker reports are
// evidence for the coordinator, which runs its own checks, so the claim note
// is added only to answers a person reads.
type workerRunKey struct{}

func inWorkerRun(ctx context.Context) bool {
	marked, _ := ctx.Value(workerRunKey{}).(bool)
	return marked
}
