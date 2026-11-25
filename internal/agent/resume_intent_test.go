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

// The reported session: three workers interrupted by a restart and two that
// failed on a model-not-found error, all listed by the coordinator as waiting.
func reportedFive() map[string][2]string {
	interrupted := [2]string{WorkerStatusInterrupted, "runner ended without a final report (app closed or crashed); resume to continue"}
	notFound := [2]string{"failed", `http 404: {"type":"error","error":{"type":"not_found_error","message":"model: ja/google/gemini-4-neon"}}`}
	return map[string][2]string{"worker-374": interrupted, "worker-377": notFound, "worker-378": notFound, "worker-379": interrupted, "worker-380": interrupted}
}

func TestUserTurnResumesWhatTheMessageRefersTo(t *testing.T) {
	all := []string{"worker-374", "worker-377", "worker-378", "worker-379", "worker-380"}
	for _, tc := range []struct {
		name, prompt string
		want         []string
	}{
		{"reported sentence", "możesz je wznowić a ogólnie da się to poscalać i potem pousuwać bo te 200 gb z kądź tam jest przecież nie?", all},
		{"polite question", "możesz je wznowić?", all},
		{"conditional politeness", "mógłbyś wznowić te workery", all},
		{"tag question", "wznów je, ok?", all},
		{"English question", "can you resume them?", all},
		{"earlier limit sentence", "dobra możesz wznowić ponownie tam te modele skończył mi się użycie skończyło się teraz jest git", all},
		{"negation", "nie wznawiaj ich", nil},
		{"negation English", "don't resume them yet", nil},
		{"leave them", "zostaw je na razie", nil},
		{"one bare number", "wznów 377", []string{"worker-377"}},
		{"named list", "wznów worker-374, 379 i 380", []string{"worker-374", "worker-379", "worker-380"}},
		{"all but one", "wznów je, ale nie 378", []string{"worker-374", "worker-377", "worker-379", "worker-380"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resumed(t, tc.prompt, reportedFive())
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("%q resumed %v, want %v", tc.prompt, got, tc.want)
			}
		})
	}
}

// A worker the user stopped is resumed only when named; the refusal tells
// the coordinator to ask.
func TestUserStoppedWorkerMustBeNamed(t *testing.T) {
	workers := map[string][2]string{
		"worker-1": {"failed", usageLimitError},
		"worker-4": {"stopped", "worker worker-4 stopped by request: context canceled"},
	}
	if got := resumed(t, "wznów je", workers); strings.Join(got, ",") != "worker-1" {
		t.Fatalf("general resume = %v, want only worker-1", got)
	}
	if got := resumed(t, "wznów też worker-4", workers); strings.Join(got, ",") != "worker-4" {
		t.Fatalf("named stopped worker = %v, want worker-4", got)
	}
	r := resumeLoop("wznów je", workers).wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: `{"to":"worker-4","message":"continue"}`})
	if !strings.Contains(r, "stopped on request") || !strings.Contains(r, "ask the user") {
		t.Fatalf("refusal = %q", r)
	}
}

// Automatic and wake turns never resume, whatever the workers' state.
func TestAutomaticTurnNeverResumes(t *testing.T) {
	l := resumeLoop("wznów je", reportedFive())
	l.wakeTurnForRun = true
	for id := range reportedFive() {
		if r := l.wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: fmt.Sprintf(`{"to":%q,"message":"continue"}`, id)}); !strings.Contains(r, "automatic turn") {
			t.Fatalf("automatic turn resumed %s: %q", id, r)
		}
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
