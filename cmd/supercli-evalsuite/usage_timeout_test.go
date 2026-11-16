package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/agenteval"
	"supercli/internal/storage/session"
)

// writeUsageRows journals model calls the way a batch run does before it
// is killed: per call, into the isolated data dir's sessions.db.
func writeUsageRows(t *testing.T, dataDir string, rows ...session.UsageRecord) {
	t.Helper()
	store, err := session.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if len(rows) == 0 {
		return
	}
	sess, err := store.Create(dataDir, "gpt-test", "eval")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range rows {
		u.SessionID = sess.ID
		if err := store.AppendUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUsageFromSessionsDBSumsJournaledCalls(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now()
	writeUsageRows(t, dataDir,
		session.UsageRecord{Model: "gpt-test", Input: 1000, CachedInput: 400, Output: 50, Reasoning: 20, DurationMS: 900, Source: "model", CreatedAt: now},
		session.UsageRecord{Model: "gpt-test", Input: 2000, CachedInput: 1500, Output: 70, Reasoning: 0, DurationMS: 1100, Source: "model", CreatedAt: now.Add(time.Second)},
	)
	m := usageFromSessionsDB(filepath.Join(dataDir, "sessions.db"))
	if m == nil {
		t.Fatal("no usage recovered from sessions.db")
	}
	if m.Input != 3000 || m.Cached != 1900 || m.Output != 120 || m.Reasoning != 20 || m.Requests != 2 || m.DurationMs != 2000 {
		t.Fatalf("recovered usage = %+v", *m)
	}
	if m.Model != "gpt-test" || !m.Partial || !m.Error || !strings.HasPrefix(m.Source, "sessions.db:") {
		t.Fatalf("recovered usage is not marked partial: %+v", *m)
	}
}

func TestUsageFromSessionsDBMissingOrEmpty(t *testing.T) {
	dir := t.TempDir()
	if m := usageFromSessionsDB(filepath.Join(dir, "sessions.db")); m != nil {
		t.Fatalf("missing db gave %+v", *m)
	}
	writeUsageRows(t, dir) // schema only, no calls
	if m := usageFromSessionsDB(filepath.Join(dir, "sessions.db")); m != nil {
		t.Fatalf("empty db gave %+v", *m)
	}
}

// TestTimedOutRunCostReachesReportLedgerAndBudget: a run that times out
// (no final usage line on stdout) keeps the usage the runner recovered, so
// the report has metrics and the ledger/budget count its cost.
func TestTimedOutRunCostReachesReportLedgerAndBudget(t *testing.T) {
	suiteDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(suiteDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(suiteDir, "fixture", "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := `{"id":"t","name":"timeout usage","tasks":[{"id":"slow","name":"slow","prompt":"p","workspaceFixture":"fixture",
		"answerChecks":[{"id":"done","anyOf":["done"]}]}]}`
	if err := os.WriteFile(filepath.Join(suiteDir, "suite.json"), []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	recovered := &agenteval.RunMetrics{Model: "gpt-test", Input: 1_000_000, Cached: 500_000, Output: 100_000, Partial: true, Error: true, Source: "sessions.db:billing_usage"}
	runner := func(ctx context.Context, in agenteval.AgentInput) (agenteval.AgentOutput, error) {
		<-ctx.Done() // the agent never finishes: the harness timeout kills it
		return agenteval.AgentOutput{Stdout: "partial assistant text", Metrics: recovered}, ctx.Err()
	}
	ledgerPath := filepath.Join(t.TempDir(), "ledger.md")
	o := options{ledger: ledgerPath, label: "unit", priceIn: 2, priceCached: 0.5, priceOut: 8, budgetStop: 1}
	l := newLedger(o)
	report, err := agenteval.Bench(context.Background(), agenteval.BenchOptions{
		SuitePath: filepath.Join(suiteDir, "suite.json"),
		WorkRoot:  t.TempDir(),
		Models:    []string{"gpt-test"},
		Timeout:   200 * time.Millisecond,
		Runner:    runner,
		OnRunDone: func(run agenteval.RunReport) { l.add(run) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("runs = %d", len(report.Runs))
	}
	run := report.Runs[0]
	if !run.TimedOut {
		t.Fatal("run did not time out")
	}
	if run.Metrics == nil || !run.Metrics.Partial || run.Metrics.Input != 1_000_000 {
		t.Fatalf("timed-out run lost its usage: %+v", run.Metrics)
	}
	// (500k uncached * $2 + 500k cached * $0.5 + 100k out * $8) / 1M = 1 + 0.25 + 0.8
	const want = 2.05
	if diff := l.total - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("ledger total = %.4f, want %.4f", l.total, want)
	}
	if !(o.budgetStop > 0 && l.total >= o.budgetStop) {
		t.Fatal("timed-out run's cost did not count toward the budget stop")
	}
	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "| slow | ") || !strings.Contains(string(data), "gpt-test (partial)") || !strings.Contains(string(data), "| 2.0500 | 2.0500 |") {
		t.Fatalf("ledger row missing for the timed-out run:\n%s", data)
	}
	// A continued ledger keeps the total across invocations.
	if again := newLedger(o); again.total < want-1e-9 {
		t.Fatalf("continued ledger total = %.4f, want %.4f", again.total, want)
	}
}

func TestFirstCallFromSessionsDBIsTheFirstModelCall(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now()
	writeUsageRows(t, dataDir,
		session.UsageRecord{Model: "gpt-test", Input: 9000, CachedInput: 0, Output: 40, DurationMS: 1500, TTFTMS: 700, HasTiming: true, Source: "model", CreatedAt: now},
		session.UsageRecord{Model: "gpt-test", Input: 9500, CachedInput: 8800, Output: 60, DurationMS: 900, TTFTMS: 300, HasTiming: true, Source: "model", CreatedAt: now.Add(time.Second)},
	)
	fc := firstCallFromSessionsDB(filepath.Join(dataDir, "sessions.db"))
	if fc == nil || fc["input"] != 9000 || fc["cached"] != 0 || fc["ttftMs"] != int64(700) || fc["durationMs"] != int64(1500) {
		t.Fatalf("first call = %v", fc)
	}
	if fc := firstCallFromSessionsDB(filepath.Join(t.TempDir(), "sessions.db")); fc != nil {
		t.Fatalf("missing db gave %v", fc)
	}
}
