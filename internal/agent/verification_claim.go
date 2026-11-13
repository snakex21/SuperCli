package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// verificationTrail remembers, per Run, whether a verification command (as
// classified by isVerificationCommand) ran and how the last one ended. It is
// evidence for one non-blocking check: a final answer that claims passing
// checks when the last check failed, or when none ran, gets a visible note.
// In the eval suite 3 of 5 failing runs ended with "Done ... verified".
type verificationTrail struct {
	mu         sync.Mutex
	ran        bool
	lastFailed bool
	command    string
}

func (v *verificationTrail) reset() {
	v.mu.Lock()
	v.ran, v.lastFailed, v.command = false, false, ""
	v.mu.Unlock()
}

func (v *verificationTrail) observe(command []string, failed bool) {
	label := strings.Join(command, " ")
	if len(label) > 80 {
		label = label[:77] + "..."
	}
	v.mu.Lock()
	v.ran, v.lastFailed, v.command = true, failed, label
	v.mu.Unlock()
}

// verificationClaimRE matches an answer stating that checks passed. English
// and Polish forms; deliberately narrow so ordinary prose does not trigger it.
var verificationClaimRE = regexp.MustCompile(`(?i)\b(?:(?:all\s+)?(?:the\s+)?(?:unit\s+)?(?:tests?|checks?|builds?|test suite)\s+(?:now\s+|still\s+|all\s+)?(?:pass(?:es|ed|ing)?|succeed(?:s|ed)?|(?:are|is)\s+(?:passing|green))|verified|verification\s+(?:passed|succeeded)|zweryfikowa\w*|(?:testy|testów)\s+(?:przechodz\w*|przeszły|zaliczon\w*)|przechodz\w*\s+(?:wszystkie\s+)?testy)\b`)

// A claim preceded by a negation in the same clause is not a claim.
var verificationNegationRE = regexp.MustCompile(`(?i)(?:\bnot|n't|\bnever|\bwithout|\bno|\bcannot|\bunable to|\bnie)\b[^.;:!?\n]{0,24}$`)

func claimsVerification(text string) bool {
	for _, loc := range verificationClaimRE.FindAllStringIndex(text, -1) {
		before := text[max(0, loc[0]-40):loc[0]]
		if verificationNegationRE.MatchString(before) {
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

var verificationFindingRE = regexp.MustCompile(`(?i)^\s+(?:that|whether|if|the\s+(?:file|path|folder|directory)|it\b|there\b|że|czy)`)

// verificationClaimWarning returns a note to append to a final answer, or "".
// It never blocks the answer and never changes what the model said.
func (l *Loop) verificationClaimWarning(text string) string {
	if !claimsVerification(text) {
		return ""
	}
	l.verifyTrail.mu.Lock()
	ran, failed, command := l.verifyTrail.ran, l.verifyTrail.lastFailed, l.verifyTrail.command
	l.verifyTrail.mu.Unlock()
	switch {
	case !ran:
		return "\n\n[SuperCli note: this answer reports passing checks, but no test or build command ran in this turn.]"
	case failed:
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
