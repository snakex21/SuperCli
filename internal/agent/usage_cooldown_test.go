package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

// The audited 429 (anonymized): a usage limit that resets in ~37 h.
const auditedUsageLimit = `http 429: {"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","plan_type":"plus","resets_at":0,"resets_in_seconds":134000}}`

func TestUsageLimitResetIsParsed(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	until, ok := usageLimitReset(auditedUsageLimit, now)
	if !ok || until.Sub(now) != 134000*time.Second {
		t.Fatalf("reset = %v %v", until, ok)
	}
	if _, ok := usageLimitReset("http 429: rate limited, retry in 2s", now); ok {
		t.Fatal("a short rate limit was taken for a usage limit")
	}
	if until, ok := usageLimitReset(`{"error":{"type":"usage_limit_reached","resets_at":1795900000}}`, now); !ok || until.Unix() != 1795900000 {
		t.Fatalf("resets_at = %v %v", until, ok)
	}
}

// After a worker hit the usage limit, new workers and resumes on that model
// are refused with the reset time instead of failing within minutes.
func TestUsageLimitedModelBlocksNewWorkersAndResumes(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"bow"}`)), say("Delegated.")},
		[][]llm.Delta{{{Err: errors.New(auditedUsageLimit)}}})
	runTurnText(t, f.parent, "Dodaj łuk.")
	w := f.task.Workers.List()[0]
	if s := w.Snapshot(); s.Status != "failed" {
		t.Fatalf("setup: worker status %s", s.Status)
	}
	defer usageCooldowns.Delete(strings.ToLower(workerRunModel(w)))
	if _, blocked := usageCooldown(workerRunModel(w), time.Now()); !blocked {
		t.Fatal("the worker model was not blocked after its usage limit")
	}
	res, _ := f.task.execute(context.Background(), json.RawMessage(`{"prompt":"another bow"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "usage limit") || !strings.Contains(res.Err.Error(), "resets at") {
		t.Fatalf("a new worker on the blocked model was not refused: %v", res.Err)
	}
	out, _ := NewSendMessageToolFor(f.task).execute(context.Background(), json.RawMessage(`{"to":"worker-1","message":"continue"}`))
	if out.Err == nil || !strings.Contains(out.Err.Error(), "usage limit") {
		t.Fatalf("a resume on the blocked model was not refused: %v", out.Err)
	}
}

// The audited failure notice repeated the raw JSON as a "partial report" and
// had no task line; now: a short reason, the reset time, the task.
func TestWorkerFailureNoticeIsShortAndNamesTheTask(t *testing.T) {
	s := Snapshot{ID: "worker-379", Agent: "code", Status: "failed", Description: "Projekt: port.\nCEL: łuk gracza"}
	cause := errors.New(auditedUsageLimit)
	text := workerFailureHandoffCaptured(s, true, auditedUsageLimit, cause, "").Error()
	if strings.Contains(text, `{"error"`) || strings.Contains(text, "Partial report") {
		t.Fatalf("raw JSON or a duplicate partial report:\n%s", text)
	}
	for _, want := range []string{"The usage limit has been reached", "resets at", "Task: łuk gracza", "worker worker-379 (code), status=failed; call failed: "} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

// The task tool tells the coordinator what each worker kind can do: an
// audited explore worker was given a job that needed ctx_execute.
func TestTaskToolDescribesWorkerKindCapabilities(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	desc := workerKindsDescription(reg)
	for _, want := range []string{"explore: read-only", "no commands", "code: reads, edits files, runs commands"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("missing %q in %s", want, desc)
		}
	}
}

// A steer to a stopped worker: in an automatic turn it is refused like any
// resume, with a clear reason.
func TestSteerOnStoppedWorkerInAutomaticTurnIsRefused(t *testing.T) {
	l := resumeLoop("", map[string][2]string{"worker-5": {"failed", "http 429"}})
	l.wakeTurnForRun = true
	r := l.wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: `{"to":"worker-5","message":"fix","mode":"steer"}`})
	if !strings.Contains(r, "automatic turn") {
		t.Fatalf("steer on a stopped worker in an automatic turn: %q", r)
	}
	running := resumeLoop("", map[string][2]string{"worker-6": {"running", ""}})
	running.wakeTurnForRun = true
	if r := running.wakeResumeRefusal(llm.ToolCall{Name: "send_message", Arguments: `{"to":"worker-6","message":"fix","mode":"steer"}`}); r != "" {
		t.Fatalf("steering a running worker was refused: %q", r)
	}
}
