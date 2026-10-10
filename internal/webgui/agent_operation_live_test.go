package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type operationLiveTurn struct {
	Prompt         string               `json:"prompt"`
	WallMS         int64                `json:"wall_ms"`
	Summary        session.TurnSummary  `json:"summary"`
	Trace          chatPhaseProbeResult `json:"trace"`
	Messages       []llm.Message        `json:"messages"`
	FinalText      string               `json:"final_text"`
	ReadBackPassed bool                 `json:"read_back_passed"`
	Passed         bool                 `json:"passed"`
}

// Opt-in real model and GUI API: local facts, two outputs, and a required
// read-back. No fake provider, scripted tool result, or evaluation-only system
// guidance is inserted. Both labels use byte-identical fixtures and prompts.
func TestAgentOperationLiveGUI(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_OPERATION_LIVE_URL"))
	if baseURL == "" {
		t.Skip("opt-in local-model operation evaluation")
	}
	u, err := url.Parse(baseURL)
	model := strings.TrimSpace(os.Getenv("SUPERCLI_OPERATION_LIVE_MODEL"))
	if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.RawQuery != "" || u.Fragment != "" || model == "" {
		t.Fatal("requires explicit loopback model URL and model name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(cwd, "..", "..", ".tmp", "agent-operation-live")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	home, data := filepath.Join(run, "workspace"), filepath.Join(run, "data")
	for _, path := range []string{home, data, filepath.Join(home, "docs"), filepath.Join(home, "config"), filepath.Join(home, "reports")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := map[string]string{
		"docs/service.md":     "# Service configuration\n\nThe Atlas service owner is Marta Nowak.\nThe health endpoint is /health/ready.\n",
		"config/runtime.json": "{\"service\":\"Atlas\",\"timeout_ms\":1250,\"retries\":2,\"port\":7341}\n",
		"jobs.csv":            "job,status,duration_ms\nimport,done,700\nindex,done,400\ncleanup,pending,250\n",
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("thinking = true\nreasoning_effort = \"max\"\nmax_steps = 12\npreflight_repo = true\nlanguage = \"pl\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previousThinking, previousEffort := llm.ThinkingEnabled(), llm.ReasoningEffort()
	llm.SetThinkingEnabled(true)
	if err := llm.SetReasoningEffort("max"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { llm.SetThinkingEnabled(previousThinking); _ = llm.SetReasoningEffort(previousEffort) })
	cfg := echoConfig()
	cfg.Provider, cfg.BaseURL, cfg.Model = config.ProviderOpenAI, baseURL, model
	cfg.APIKey, cfg.Stream, cfg.MaxTokens, cfg.Timeout = "", true, 8192, 180*time.Second
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(cfg, home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	provider := &downloadLiveProvider{inner: eng.prov}
	eng.prov = provider
	handler := NewServer(eng, false).Handler()
	report := struct {
		Label     string                `json:"label"`
		Model     string                `json:"model"`
		Run       string                `json:"run"`
		Thinking  bool                  `json:"thinking"`
		SessionID string                `json:"session_id"`
		Fixtures  map[string]string     `json:"fixtures"`
		Turns     []operationLiveTurn   `json:"turns"`
		Requests  []downloadLiveRequest `json:"requests"`
		Outputs   map[string]string     `json:"outputs"`
		Passed    bool                  `json:"passed"`
	}{Label: os.Getenv("SUPERCLI_OPERATION_LIVE_LABEL"), Model: model, Run: run, Thinking: true, Fixtures: fixtures, Outputs: map[string]string{}}
	defer func() {
		provider.mu.Lock()
		report.Requests = append([]downloadLiveRequest(nil), provider.requests...)
		provider.mu.Unlock()
		report.Passed = !t.Failed() && len(report.Turns) == 2
		b, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(run, "receipt.json"), b, 0600)
		}
		if err != nil {
			t.Errorf("save operation receipt: %v", err)
		}
		t.Logf("operation receipt: %s", filepath.Join(run, "receipt.json"))
	}()
	prompts := []string{
		"Sprawdź lokalne pliki docs/service.md, config/runtime.json i jobs.csv. Podaj właściciela usługi, port, łączny czas zakończonych zadań oraz maksymalny czas wszystkich prób żądania (timeout razy pierwsza próba plus ponowienia). Niczego nie zapisuj.",
		"Na podstawie tych danych utwórz dwa pliki: reports/summary.json z polami owner, port, completed_ms i request_budget_ms oraz reports/summary.txt z tymi samymi czterema wartościami. Po zapisaniu odczytaj oba pliki i sprawdź ich zgodność z danymi. W odpowiedzi podaj oba zapisane pliki i wynik weryfikacji.",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	lastSeq := -1
	for i, prompt := range prompts {
		trace, sid := chatPhaseProbeTurn(t, handler, ctx, prompt, report.SessionID, fmt.Sprintf("operation-live-%s-%d", filepath.Base(run), i), false, true)
		report.SessionID = sid
		turn := operationLiveTurn{Prompt: prompt, WallMS: trace.point("receipt_return") / int64(time.Millisecond), Trace: trace.result("local-qwen-gui", "general-operations")}
		store, err := eng.sessionStore()
		if err != nil {
			t.Fatal(err)
		}
		summaries, err := store.ReadTurnSummaries(ctx, sid)
		if err != nil || len(summaries) == 0 {
			t.Fatalf("turn summary missing: %v", err)
		}
		turn.Summary = summaries[len(summaries)-1]
		encoded, err := store.ReadMessages(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range encoded {
			if row.Seq > lastSeq {
				msg, err := row.ToMessage()
				if err != nil {
					t.Fatal(err)
				}
				turn.Messages = append(turn.Messages, msg)
			}
		}
		if len(encoded) > 0 {
			lastSeq = encoded[len(encoded)-1].Seq
		}
		if len(turn.Messages) > 0 {
			last := turn.Messages[len(turn.Messages)-1]
			if last.Role == llm.RoleAssistant {
				turn.FinalText = strings.TrimSpace(regexp.MustCompile(`(?s)<(?:thinking|think)>.*?</(?:thinking|think)>`).ReplaceAllString(last.TextOnly().Content, ""))
			}
		}
		turn.Passed = turn.Summary.ToolDiag.Terminal == "" && turn.FinalText != ""
		if i == 0 {
			for _, value := range []string{"Marta Nowak", "7341", "1100", "3750"} {
				if !strings.Contains(strings.ReplaceAll(turn.FinalText, " ", ""), strings.ReplaceAll(value, " ", "")) {
					turn.Passed = false
				}
			}
		}
		if i == 1 {
			for _, name := range []string{"reports/summary.json", "reports/summary.txt"} {
				b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
				if err != nil {
					turn.Passed = false
					continue
				}
				report.Outputs[name] = string(b)
				for _, value := range []string{"Marta Nowak", "7341", "1100", "3750"} {
					if !strings.Contains(strings.ReplaceAll(string(b), " ", ""), strings.ReplaceAll(value, " ", "")) {
						turn.Passed = false
					}
				}
			}
			var parsed struct {
				Owner       string `json:"owner"`
				Port        int    `json:"port"`
				CompletedMS int    `json:"completed_ms"`
				BudgetMS    int    `json:"request_budget_ms"`
			}
			if err := json.Unmarshal([]byte(report.Outputs["reports/summary.json"]), &parsed); err != nil || parsed.Owner != "Marta Nowak" || parsed.Port != 7341 || parsed.CompletedMS != 1100 || parsed.BudgetMS != 3750 {
				turn.Passed = false
			}
			turn.ReadBackPassed = operationLiveReadBack(turn.Messages)
			turn.Passed = turn.Passed && turn.ReadBackPassed
		}
		report.Turns = append(report.Turns, turn)
		t.Logf("operation turn %d: %.3fs, %d model calls, %d tools, %d failed", i+1, float64(turn.WallMS)/1000, turn.Summary.ModelCalls, turn.Summary.ToolCalls, turn.Summary.ToolFailures)
		if !turn.Passed {
			t.Fatalf("operation turn %d failed; transcript in durable receipt", i+1)
		}
	}
}

// Receipt validation only: require successful creations before the native
// file readers (or an equivalent command) return the four actual values.
func operationLiveReadBack(messages []llm.Message) bool {
	calls := map[string]llm.ToolCall{}
	created, read := map[string]bool{}, map[string]bool{}
	paths := []string{"reports/summary.json", "reports/summary.txt"}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			calls[call.ID] = call
		}
		if msg.Role != llm.RoleTool {
			continue
		}
		call := calls[msg.ToolCallID]
		args := strings.ReplaceAll(strings.ReplaceAll(call.Arguments, `\\`, "/"), `\`, "/")
		if call.Name == "create_file" || call.Name == "write_file" {
			if strings.HasPrefix(msg.Content, "Created ") || strings.HasPrefix(msg.Content, "Overwrote ") {
				for _, path := range paths {
					if strings.Contains(args, path) {
						created[path] = true
					}
				}
			}
		}
		if call.Name != "read_many" && call.Name != "read_lines" && call.Name != "ctx_execute" {
			continue
		}
		if !strings.Contains(msg.Content, "Marta Nowak") || !strings.Contains(msg.Content, "7341") || !strings.Contains(msg.Content, "1100") || !strings.Contains(msg.Content, "3750") {
			continue
		}
		for _, path := range paths {
			if created[path] && strings.Contains(args, path) {
				read[path] = true
			}
		}
	}
	return read[paths[0]] && read[paths[1]]
}
