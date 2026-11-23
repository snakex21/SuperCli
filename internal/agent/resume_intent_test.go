package agent

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// resumeLoop is a user-turn loop with the given workers (id -> status, error).
func resumeLoop(prompt string, workers map[string][2]string) *Loop {
	l := &Loop{userPromptForRun: prompt}
	l.workerStatusSource = func() []Snapshot {
		var out []Snapshot
		for id, s := range workers {
			out = append(out, Snapshot{ID: id, Status: s[0], LastError: s[1]})
		}
		return out
	}
	return l
}

// resumed returns the workers a turn with prompt may resume, sorted.
func resumed(t *testing.T, prompt string, workers map[string][2]string) []string {
	t.Helper()
	l := resumeLoop(prompt, workers)
	var out []string
	for id := range workers {
		tc := llm.ToolCall{Name: "send_message", Arguments: fmt.Sprintf(`{"to":%q,"message":"continue"}`, id)}
		if l.wakeResumeRefusal(tc) == "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

const usageLimitError = "anthropic: usage limit reached (HTTP 429): usage_limit_error"

// The five workers of the reported session, all stopped by the usage limit.
func limitedFive() map[string][2]string {
	w := map[string][2]string{}
	for _, id := range []string{"worker-363", "worker-376", "worker-379", "worker-380", "worker-381"} {
		w[id] = [2]string{"failed", usageLimitError}
	}
	return w
}

func TestResumeAfterUsageLimitFollowsThePlainRequest(t *testing.T) {
	all := []string{"worker-363", "worker-376", "worker-379", "worker-380", "worker-381"}
	for _, tc := range []struct {
		name, prompt string
		want         []string
	}{
		{"reported Polish sentence", "dobra możesz wznowić ponownie tam te modele skończył mi się użycie skończyło się teraz jest git", all},
		{"short plural", "wznów je", all},
		{"continue the workers", "kontynuuj workery", all},
		{"English plural", "the limit reset, please resume them", all},
		{"English can-you", "can you continue the workers now", all},
		{"negation", "nie wznawiaj ich", nil},
		{"negation English", "don't resume them yet", nil},
		{"one bare number", "wznów 363", []string{"worker-363"}},
		{"named list", "wznów worker-363, 376 i 381", []string{"worker-363", "worker-376", "worker-381"}},
		{"all but one", "wznów je, ale nie 376", []string{"worker-363", "worker-379", "worker-380", "worker-381"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resumed(t, tc.prompt, limitedFive())
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("%q resumed %v, want %v", tc.prompt, got, tc.want)
			}
		})
	}
}

// A general request covers limit, connection and restart stops, not a worker
// the user stopped or a genuine task failure; naming one includes it.
func TestPluralResumeLeavesUserStoppedAndFailedTasks(t *testing.T) {
	workers := map[string][2]string{
		"worker-1": {"failed", usageLimitError},
		"worker-2": {"failed", "sse: stream error: INTERNAL_ERROR: connection reset by peer"},
		"worker-3": {WorkerStatusInterrupted, "process restarted"},
		"worker-4": {"stopped", "worker worker-4 stopped by request: context canceled"},
		"worker-5": {"failed", "tests still fail after 5 attempts: assertion mismatch in mobs_test.go"},
	}
	if got := resumed(t, "wznów je", workers); strings.Join(got, ",") != "worker-1,worker-2,worker-3" {
		t.Fatalf("plural resume = %v, want the limit, connection and restart stops only", got)
	}
	if got := resumed(t, "resume them all and also worker-4", workers); strings.Join(got, ",") != "worker-4" {
		// Naming narrows: the user named only worker-4 here.
		t.Fatalf("named resume = %v, want worker-4", got)
	}
	if got := resumed(t, "wznów 4 i 5", workers); strings.Join(got, ",") != "worker-4,worker-5" {
		t.Fatalf("named user-stopped and failed workers = %v, want both", got)
	}
	l := resumeLoop("wznów je", workers)
	r := l.wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: `{"to":"worker-4","message":"continue"}`})
	if !strings.Contains(r, "stopped on request") || !strings.Contains(r, "ask the user") {
		t.Fatalf("user-stopped worker refusal = %q", r)
	}
	r = l.wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: `{"to":"worker-5","message":"continue"}`})
	if !strings.Contains(r, "failed on its task") || !strings.Contains(r, "assertion mismatch") {
		t.Fatalf("genuine failure refusal = %q", r)
	}
}

// A usage-limit stop needs no special words in a user turn; a question or an
// unrelated message does not resume other stops; automatic turns never do.
func TestUsageLimitStopNeedsNoSpecialWords(t *testing.T) {
	workers := map[string][2]string{
		"worker-1": {"failed", usageLimitError},
		"worker-2": {"failed", "rate limited (HTTP 429): too many requests"},
		"worker-3": {"stopped", "stopped by request"},
		"worker-4": {WorkerStatusInterrupted, ""},
	}
	if got := resumed(t, "ok, limit już jest", workers); strings.Join(got, ",") != "worker-1,worker-2" {
		t.Fatalf("plain message = %v, want the usage-limited workers", got)
	}
	if got := resumed(t, "dlaczego nie wznowiłeś worker-4?", workers); strings.Join(got, ",") != "worker-1,worker-2" {
		t.Fatalf("question = %v, want only the usage-limited workers", got)
	}
	if got := resumed(t, "nie wznawiaj nic", workers); len(got) != 0 {
		t.Fatalf("negation = %v, want none", got)
	}
	l := resumeLoop("wznów je", workers)
	l.wakeTurnForRun = true
	if r := l.wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: `{"to":"worker-1","message":"continue"}`}); !strings.Contains(r, "automatic turn") {
		t.Fatalf("automatic turn resumed a usage-limited worker: %q", r)
	}
}

// The "Resume all (N)" button's message names the workers in every UI
// language, so the gate accepts it.
func TestResumeAllButtonPromptIsAccepted(t *testing.T) {
	workers := map[string][2]string{
		"worker-7": {"failed", "connection reset"},
		"worker-8": {WorkerStatusInterrupted, ""},
	}
	for lang, prompt := range map[string]string{
		"pl": "Wznów worker-7, worker-8 przez send_message i kontynuuj ich zadania od miejsca przerwania; ich zapisane rozmowy zostaną przywrócone.",
		"en": "Resume worker-7, worker-8 with send_message and continue their tasks from where they stopped; their saved conversations are restored.",
		"de": "Setze worker-7, worker-8 mit send_message fort und führe ihre Aufgaben dort weiter, wo sie angehalten haben.",
		"tr": "worker-7, worker-8 ajanlarını send_message ile sürdür ve görevlerine kaldıkları yerden devam et.",
	} {
		if got := resumed(t, prompt, workers); strings.Join(got, ",") != "worker-7,worker-8" {
			t.Errorf("%s button prompt resumed %v", lang, got)
		}
	}
}

func TestClassifyStop(t *testing.T) {
	for _, tc := range []struct {
		status, err string
		want        stopKind
	}{
		{"failed", usageLimitError, stopLimit},
		{"failed", "openai: 429 Too Many Requests", stopLimit},
		{"failed", "You have reached your subscription limit", stopLimit},
		{"failed", "dial tcp: i/o timeout", stopTransient},
		{"failed", "overloaded_error (529)", stopTransient},
		{WorkerStatusInterrupted, "", stopTransient},
		{"stopped", "stopped by request", stopByUser},
		{"failed", "dead end: 5 identical file-tool failures", stopTaskFailure},
		{"failed", "context canceled", stopTaskFailure},
	} {
		if got := classifyStop(tc.status, tc.err); got != tc.want {
			t.Errorf("classifyStop(%q, %q) = %v, want %v", tc.status, tc.err, got, tc.want)
		}
	}
}
