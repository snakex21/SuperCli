package agent

// Opt-in end-to-end workload for the durable Goal contract on a real
// OpenAI-compatible backend. A trial starts with an active persisted goal and
// one in-progress task. The model must repair a deterministic Go bug, run the
// test, complete the task, record verification evidence, and close the goal.
// The harness then reopens SQLite and independently verifies both the code and
// the durable goal state.
//
// Required:
//
//   SUPERCLI_EVAL_GOAL_URL / SUPERCLI_EVAL_GOAL_MODEL
//
// Optional:
//
//   SUPERCLI_EVAL_GOAL_TRIALS (default 3, maximum 10)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	store "supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

type liveGoalTrial struct {
	Trial            int              `json:"trial"`
	Passed           bool             `json:"passed"`
	DurationMS       int64            `json:"duration_ms"`
	Turns            stats.Total      `json:"turns"`
	PhasesUS         map[string]int64 `json:"phases_us"`
	ModelCalls       int              `json:"model_calls"`
	ToolCalls        int              `json:"tool_calls"`
	ToolFailures     int              `json:"tool_failures"`
	ProtocolFailures int              `json:"protocol_failures"`
	ToolSequence     []string         `json:"tool_sequence,omitempty"`
	GoalActions      []string         `json:"goal_actions,omitempty"`
	FailedTools      []string         `json:"failed_tools,omitempty"`
	GoalStatus       string           `json:"goal_status,omitempty"`
	TaskStatus       string           `json:"task_status,omitempty"`
	Verification     string           `json:"verification,omitempty"`
	Evidence         string           `json:"evidence,omitempty"`
	PersistenceOK    bool             `json:"persistence_ok"`
	IndependentOK    bool             `json:"independent_test_ok"`
	RequiredOrder    bool             `json:"required_order_ok"`
	Error            string           `json:"error,omitempty"`
}

func TestGoalWorkload_Live(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_EVAL_GOAL_URL"))
	model := strings.TrimSpace(os.Getenv("SUPERCLI_EVAL_GOAL_MODEL"))
	if baseURL == "" || model == "" {
		t.Skip("live goal workload: set SUPERCLI_EVAL_GOAL_URL and SUPERCLI_EVAL_GOAL_MODEL")
	}
	trials := 3
	if raw := strings.TrimSpace(os.Getenv("SUPERCLI_EVAL_GOAL_TRIALS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 10 {
			trials = n
		}
	}

	results := make([]liveGoalTrial, 0, trials)
	for i := 1; i <= trials; i++ {
		result := runLiveGoalTrial(t, i, baseURL, model)
		results = append(results, result)
		b, _ := json.Marshal(result)
		t.Log(string(b))
	}

	var passed, modelCalls, toolCalls, toolFailures int
	var durationMS, backendUS, streamUS, cliUS, toolsUS int64
	for _, result := range results {
		if result.Passed {
			passed++
		}
		modelCalls += result.ModelCalls
		toolCalls += result.ToolCalls
		toolFailures += result.ToolFailures
		durationMS += result.DurationMS
		backendUS += result.PhasesUS[stats.PhaseBackendWait]
		streamUS += result.PhasesUS[stats.PhaseStreamTotal]
		cliUS += result.PhasesUS[stats.PhaseContextPrepare] + result.PhasesUS[stats.PhaseNextTurnPrepare]
		toolsUS += result.PhasesUS[stats.PhaseToolExecution]
	}
	summary := map[string]any{
		"trials": trials, "passed": passed, "success_percent": passed * 100 / trials,
		"average_duration_ms": durationMS / int64(trials),
		"average_model_calls": float64(modelCalls) / float64(trials),
		"average_tool_calls":  float64(toolCalls) / float64(trials),
		"tool_failures":       toolFailures, "backend_wait_ms": backendUS / 1000,
		"stream_ms": streamUS / 1000, "cli_ms": cliUS / 1000, "tools_ms": toolsUS / 1000,
	}
	b, _ := json.Marshal(summary)
	t.Logf("GOAL_WORKLOAD_SUMMARY %s", b)
	if passed != trials {
		t.Fatalf("live goal workload passed %d/%d trials", passed, trials)
	}
}

func runLiveGoalTrial(t *testing.T, trial int, baseURL, model string) liveGoalTrial {
	t.Helper()
	result := liveGoalTrial{Trial: trial, PhasesUS: map[string]int64{}}
	home := t.TempDir()
	if err := writeLiveGoalFixture(home); err != nil {
		result.Error = err.Error()
		return result
	}

	setupCtx := context.Background()
	db, err := store.Open(home)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	goalStore := goal.NewStorage(db)
	if err := goalStore.Migrate(setupCtx); err != nil {
		_ = db.Close()
		result.Error = err.Error()
		return result
	}
	goalSvc := goal.NewService(goalStore)
	created, err := goalSvc.Set(setupCtx,
		"Napraw funkcję Add i potwierdź wynik testem",
		"W mathutil.go jest pojedynczy błąd implementacji. Nie zmieniaj testu.",
		"go test ./... kończy się sukcesem, a Add(2, 3) zwraca 5.", "live-goal-eval")
	if err != nil {
		_ = db.Close()
		result.Error = err.Error()
		return result
	}
	task, err := goalSvc.AddTask(setupCtx, created.ID, "Odczytaj pliki, napraw Add i uruchom go test ./...")
	if err == nil {
		err = goalSvc.SetTaskStatus(setupCtx, created.ID, task.Seq, goal.TaskInProgress)
	}
	if err != nil {
		_ = db.Close()
		result.Error = err.Error()
		return result
	}
	system, err := goalSvc.Inject(setupCtx,
		"You are a coding agent. Use tools to inspect, modify, and verify the workspace. Follow the active Goal lifecycle exactly. Keep the final answer brief and factual.", 5)
	if err != nil {
		_ = db.Close()
		result.Error = err.Error()
		return result
	}

	recorder := stats.NewMemory()
	inner, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, Timeout: 8 * time.Minute})
	if err != nil {
		_ = db.Close()
		result.Error = err.Error()
		return result
	}
	provider := llm.Metered(inner, "local-goal-workload", llm.PurposeMain, func(s llm.CallStat) {
		recorder.RecordCall(stats.Call{Purpose: s.Purpose, Model: s.Model, Provider: s.Provider,
			TTFTUs: s.TTFT.Microseconds(), DurationUs: s.Duration.Microseconds(), TokensIn: s.TokensIn,
			TokensOut: s.TokensOut, Failed: s.Failed, Canceled: s.Canceled})
	})
	registry := tools.NewRegistry()
	for _, spec := range []tools.Tool{
		tools.NewReadMany(home).Spec(), tools.NewPatchFile(home).Spec(),
		tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec(),
	} {
		registry.MustRegister(spec)
		registry.MarkAlwaysOn(spec.Name)
	}
	// Mirror production: an already-active Goal is visible immediately. Its
	// stable schema is cached, avoiding a separate discovery inference for each
	// state transition. With no active goal, production leaves it discoverable.
	goalSpec := tools.NewGoalTool(goalSvc).Spec()
	registry.MustRegister(goalSpec)
	registry.MarkAlwaysOn(goalSpec.Name)
	searcher := tools.NewToolSearcher(registry, nil).Spec()
	registry.MustRegister(searcher)
	registry.MarkAlwaysOn(searcher.Name)
	invoke := NewInvokeTool(registry).Spec()
	registry.MustRegister(invoke)
	registry.MarkAlwaysOn(invoke.Name)

	loop, err := NewLoop(LoopConfig{
		Provider: provider, Registry: registry, BaseDir: home, Stats: recorder, MaxSteps: 14,
		System: system, ThinTools: true, StableToolset: true,
	})
	if err != nil {
		_ = db.Close()
		result.Error = err.Error()
		return result
	}

	started := time.Now()
	runCtx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	ch, runErr := loop.Run(runCtx, "Zrealizuj aktywny cel do końca. Odczytaj oba pliki jednym read_many, popraw tylko mathutil.go i uruchom go test ./... przez ctx_execute. Następnie przez narzędzie goal wykonaj kolejno akcje complete_task, verify z konkretnym wynikiem testu oraz mark_done. Nazwy akcji Goal nie są osobnymi narzędziami. Nie pomijaj żadnego etapu.")
	toolNames := map[string]string{}
	toolArgs := map[string]string{}
	goalClosed := false
	editSeen := false
	successfulCtxAfterEdit := false
	if runErr == nil {
		for event := range ch {
			switch e := event.(type) {
			case ToolCallEvent:
				result.ToolCalls++
				result.ToolSequence = append(result.ToolSequence, e.Name)
				toolNames[e.ID] = e.Name
				toolArgs[e.ID] = e.Args
				if e.Name == "patch_file" {
					editSeen = true
				}
				if e.Name == "goal" {
					var args struct {
						Action string `json:"action"`
					}
					if json.Unmarshal([]byte(e.Args), &args) == nil && args.Action != "" {
						result.GoalActions = append(result.GoalActions, args.Action)
					}
				}
			case ToolResultEvent:
				if e.Err != nil {
					result.ToolFailures++
					if toolNames[e.ID] != "ctx_execute" {
						result.ProtocolFailures++
					}
					result.FailedTools = append(result.FailedTools,
						fmt.Sprintf("%s %s: %v", toolNames[e.ID], toolArgs[e.ID], e.Err))
				} else if toolNames[e.ID] == "ctx_execute" && editSeen {
					successfulCtxAfterEdit = true
				} else if toolNames[e.ID] == "goal" {
					var args struct {
						Action string `json:"action"`
					}
					if json.Unmarshal([]byte(toolArgs[e.ID]), &args) == nil && args.Action == "mark_done" {
						// Measure time-to-durable-completion. The optional prose call after
						// mark_done is not part of the Goal contract and can loop on local
						// models, just like the final prose after a green ctx_execute.
						goalClosed = true
						cancel()
					}
				}
			case ErrorEvent:
				if e.Err != nil && !goalClosed {
					runErr = e.Err
				}
			}
		}
	}
	cancel()
	result.DurationMS = time.Since(started).Milliseconds()
	result.Turns = stats.Sum(recorder.Snapshot())
	result.PhasesUS = stats.SumPhases(recorder.Snapshot())
	for _, call := range stats.SumCalls(recorder.Calls()) {
		result.ModelCalls += call.Count
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}

	// Reopen the database to prove Goal state is durable rather than merely
	// correct in the service's in-memory active pointer.
	if err := db.Close(); err != nil && result.Error == "" {
		result.Error = err.Error()
	}
	reopened, err := store.Open(home)
	if err != nil {
		result.Error = appendLiveGoalError(result.Error, "reopen goal db: "+err.Error())
		return result
	}
	defer reopened.Close()
	reopenedStore := goal.NewStorage(reopened)
	if err := reopenedStore.Migrate(setupCtx); err != nil {
		result.Error = appendLiveGoalError(result.Error, "remigrate goal db: "+err.Error())
		return result
	}
	reopenedSvc := goal.NewService(reopenedStore)
	active, refreshErr := reopenedSvc.Refresh(setupCtx)
	persistedGoal, goalErr := reopenedSvc.Goal(setupCtx, created.ID)
	persistedTasks, tasksErr := reopenedSvc.ListTasks(setupCtx, created.ID)
	if refreshErr != nil || goalErr != nil || tasksErr != nil || persistedGoal == nil || len(persistedTasks) != 1 {
		result.Error = appendLiveGoalError(result.Error,
			fmt.Sprintf("persisted state: refresh=%v goal=%v tasks=%v goal_nil=%v task_count=%d",
				refreshErr, goalErr, tasksErr, persistedGoal == nil, len(persistedTasks)))
		return result
	}
	result.GoalStatus = string(persistedGoal.Status)
	result.TaskStatus = string(persistedTasks[0].Status)
	result.Verification = string(persistedGoal.VerificationStatus)
	result.Evidence = persistedGoal.VerificationEvidence
	result.PersistenceOK = active == nil && persistedGoal.Status == goal.StatusDone &&
		persistedTasks[0].Status == goal.TaskDone && persistedGoal.VerificationStatus == goal.VerificationPassed &&
		strings.TrimSpace(persistedGoal.VerificationEvidence) != ""

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer verifyCancel()
	cmd := exec.CommandContext(verifyCtx, "go", "test", "./...")
	cmd.Dir = home
	output, verifyErr := cmd.CombinedOutput()
	production, readErr := os.ReadFile(filepath.Join(home, "mathutil.go"))
	result.IndependentOK = verifyErr == nil && readErr == nil && strings.Contains(string(production), "return a + b")
	if !result.IndependentOK {
		result.Error = appendLiveGoalError(result.Error,
			fmt.Sprintf("independent verification: test=%v output=%s read=%v", verifyErr, strings.TrimSpace(string(output)), readErr))
	}
	result.RequiredOrder = liveGoalRequiredOrder(result.ToolSequence, result.GoalActions) && successfulCtxAfterEdit
	if !result.RequiredOrder {
		result.Error = appendLiveGoalError(result.Error,
			fmt.Sprintf("required lifecycle missing or out of order: tools=%v goal_actions=%v", result.ToolSequence, result.GoalActions))
	}
	result.Passed = result.Error == "" && result.PersistenceOK && result.IndependentOK && result.RequiredOrder && result.ProtocolFailures == 0
	return result
}

func writeLiveGoalFixture(home string) error {
	files := map[string]string{
		"go.mod":           "module goalworkload\n\ngo 1.22\n",
		"mathutil.go":      "package goalworkload\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n",
		"mathutil_test.go": "package goalworkload\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 { t.Fatalf(\"Add(2, 3) = %d, want 5\", got) }\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func liveGoalRequiredOrder(toolsUsed, actions []string) bool {
	toolOrder := []string{"read_many", "patch_file", "ctx_execute"}
	goalOrder := []string{"complete_task", "verify", "mark_done"}
	return containsOrdered(toolsUsed, toolOrder) && containsOrdered(actions, goalOrder)
}

func containsOrdered(have, want []string) bool {
	next := 0
	for _, value := range have {
		if next < len(want) && value == want[next] {
			next++
		}
	}
	return next == len(want)
}

func appendLiveGoalError(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}
