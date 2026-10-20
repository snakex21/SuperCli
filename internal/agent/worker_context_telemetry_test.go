package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

type workerContextTelemetryProvider struct{ requests [][]llm.Message }

func (*workerContextTelemetryProvider) Name() string { return "worker-context-fixture" }
func (p *workerContextTelemetryProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Role: llm.RoleAssistant, Content: "fixture reply"}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestWorkerContextTelemetryAppendAndRetransmit(t *testing.T) {
	rec := stats.NewMemory()
	provider := &workerContextTelemetryProvider{}
	loop := makeStatsLoop(t, meteredStatsProvider(provider, rec), echoToolRegistry(t), rec)
	report := strings.Repeat("PRIVATE_WORKER_BODY source.go:17 ą日本語\n", 60)
	encoded := workerReportCompleteModelTextFromSnapshot(Snapshot{ID: "worker-1", Agent: "code", Status: "done"}, "code done · 2 steps", report, "")
	if encoded == "" || len(encoded) >= len(report) {
		t.Fatal("fixture did not exercise compact worker-report projection")
	}
	ctx := context.Background()
	appendReport := func() {
		loop.appendBackgroundMessages(ctx, []backgroundMessage{{content: encoded, scoped: true, epoch: loop.conversationEpoch.Load()}})
	}
	appendReport()
	// A human/generic message that looks exactly like a worker report has no
	// host provenance, so it must not inflate these counters.
	loop.appendBackgroundMessages(ctx, []backgroundMessage{{content: encoded}})
	if len(provider.requests) != 0 || len(rec.Snapshot()) != 0 {
		t.Fatal("recording additions initiated a request or a synthetic stats step")
	}
	for step := 1; step <= 3; step++ {
		if step == 3 {
			appendReport() // Same bytes are a genuinely new append, not a hash duplicate.
		}
		started := time.Now()
		loop.statsStartStep(step)
		_, _, _, err := loop.completeOnce(ctx, loop.prepareToolRequest(), make(chan Event, 128))
		if err != nil {
			t.Fatal(err)
		}
		loop.statsEndStep(started)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider calls = %d, want exactly 3", len(provider.requests))
	}
	for i, request := range provider.requests {
		count := 0
		for _, message := range request {
			if message.Name == llm.WorkerNotificationName {
				count++
				if message.Content != encoded {
					t.Fatalf("request %d changed the compact report", i)
				}
			}
		}
		want := 1
		if i == 2 {
			want = 2
		}
		if count != want {
			t.Fatalf("request %d worker messages = %d, want %d", i, count, want)
		}
	}
	got := stats.SumWorkerContext(rec.Snapshot(), rec.Calls())
	want := stats.WorkerContext{AddedMessages: 2, AddedBytes: int64(2 * len(encoded)), Requests: 3, MessageExposures: 4, ContextBytes: int64(4 * len(encoded))}
	if got != want {
		t.Fatalf("worker telemetry = %+v, want %+v", got, want)
	}
	data, err := json.Marshal(struct {
		Turns []stats.Turn `json:"turns"`
		Calls []stats.Call `json:"calls"`
	}{rec.Snapshot(), rec.Calls()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_WORKER_BODY") || strings.Contains(string(data), encoded) {
		t.Fatal("telemetry retained worker report contents")
	}
}

func TestWorkerContextTelemetryEpochAndExistingHistory(t *testing.T) {
	rec := stats.NewMemory()
	provider := &workerContextTelemetryProvider{}
	loop := makeStatsLoop(t, meteredStatsProvider(provider, rec), echoToolRegistry(t), rec)
	ctx := context.Background()
	loop.appendBackgroundMessages(ctx, []backgroundMessage{{content: "old report", scoped: true, epoch: loop.conversationEpoch.Load()}})
	loop.conversationEpoch.Add(1)
	loop.Messages = []llm.Message{{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "restored report ą"}}
	// A late report for the old conversation is not a new append in this one.
	loop.appendBackgroundMessages(ctx, []backgroundMessage{{content: "foreign", scoped: true, epoch: loop.conversationEpoch.Load() - 1}})
	started := time.Now()
	loop.statsStartStep(1)
	_, _, _, err := loop.completeOnce(ctx, loop.prepareToolRequest(), make(chan Event, 128))
	if err != nil {
		t.Fatal(err)
	}
	loop.statsEndStep(started)
	got := stats.SumWorkerContext(rec.Snapshot(), rec.Calls())
	if got.AddedMessages != 0 || got.AddedBytes != 0 || got.Requests != 1 || got.MessageExposures != 1 || got.ContextBytes != int64(len("restored report ą")) {
		t.Fatalf("restored/foreign history misattributed: %+v", got)
	}
}
