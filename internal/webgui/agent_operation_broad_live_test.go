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

type broadOperationLiveTurn struct {
	Prompt                    string               `json:"prompt"`
	WallMS                    int64                `json:"wall_ms"`
	Summary                   session.TurnSummary  `json:"summary"`
	Trace                     chatPhaseProbeResult `json:"trace"`
	Messages                  []llm.Message        `json:"messages"`
	FinalText                 string               `json:"final_text"`
	IndependentChecksTogether bool                 `json:"independent_checks_together"`
	VerificationAfterEdit     bool                 `json:"verification_after_edit"`
	ToolsAfterCompletion      int                  `json:"tools_after_completion"`
	RepeatedSuccessfulCalls   int                  `json:"repeated_successful_calls"`
	Passed                    bool                 `json:"passed"`
}

// Opt-in actual GUI/model evaluation. The checks are installed local Node CLI
// programs with real exit codes, not fake tool results. Candidate labels alter
// only the receipt, and ordinary package tests never contact a model.
func TestAgentOperationBroadLiveGUI(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_BROAD_OPERATION_LIVE_URL"))
	if baseURL == "" {
		t.Skip("opt-in local-model broad operation evaluation")
	}
	u, err := url.Parse(baseURL)
	model := strings.TrimSpace(os.Getenv("SUPERCLI_BROAD_OPERATION_LIVE_MODEL"))
	if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.RawQuery != "" || u.Fragment != "" || model == "" {
		t.Fatal("requires explicit loopback model URL and model name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(cwd, "..", "..", ".tmp", "smart-operation-fix", "runs")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	home, data := filepath.Join(run, "workspace"), filepath.Join(run, "data")
	for _, path := range []string{home, data, filepath.Join(home, "checks"), filepath.Join(home, "services", "runtime"), filepath.Join(home, "services", "examples")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := map[string]string{
		"capacity.json":                 "{\"slots\":17,\"reserved\":6}\n",
		"owner.json":                    "{\"owner\":\"Marek Lis\",\"service\":\"Orion\"}\n",
		"checks/owner.cjs":              "const fs=require('fs'); const x=JSON.parse(fs.readFileSync('owner.json','utf8')); console.log('OWNER_CHECK_OK owner='+x.owner+' service='+x.service);\n",
		"checks/capacity.cjs":           "const fs=require('fs'); const x=JSON.parse(fs.readFileSync('capacity.json','utf8')); if(x.reserved>x.slots)process.exit(1); console.log('CAPACITY_CHECK_OK available='+(x.slots-x.reserved));\n",
		"services/runtime/live.json":    "{\"service\":\"Orion\",\"retries\":2,\"timeout_ms\":1400,\"port\":8234}\n",
		"services/examples/sample.json": "{\"service\":\"Demo\",\"retries\":1,\"timeout_ms\":500,\"port\":8000}\n",
		"checks/retry.cjs":              "const fs=require('fs');const x=JSON.parse(fs.readFileSync('services/runtime/live.json','utf8'));if(x.service!=='Orion'||x.retries!==4||x.timeout_ms!==1400||x.port!==8234){console.error('RETRY_CHECK_FAILED '+JSON.stringify(x));process.exit(1);}console.log('RETRY_CHECK_OK service=Orion retries=4 timeout_ms=1400 port=8234');\n",
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
		Label           string                   `json:"label"`
		Model           string                   `json:"model"`
		Run             string                   `json:"run"`
		Thinking        bool                     `json:"thinking"`
		ReasoningEffort string                   `json:"reasoning_effort"`
		Fixtures        map[string]string        `json:"fixtures"`
		SessionID       string                   `json:"session_id"`
		Turns           []broadOperationLiveTurn `json:"turns"`
		Requests        []downloadLiveRequest    `json:"requests"`
		Passed          bool                     `json:"passed"`
	}{Label: os.Getenv("SUPERCLI_BROAD_OPERATION_LIVE_LABEL"), Model: model, Run: run, Thinking: true, ReasoningEffort: "max", Fixtures: fixtures}
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
			t.Errorf("save broad operation receipt: %v", err)
		}
		t.Logf("broad operation receipt: %s", filepath.Join(run, "receipt.json"))
	}()
	prompts := []string{
		"Uruchom dwie niezależne lokalne kontrole CLI: node checks/owner.cjs oraz node checks/capacity.cjs. Podaj ustalonego właściciela usługi i liczbę dostępnych miejsc. Wystarczy wynik tych kontroli; niczego nie zmieniaj.",
		"Znajdź w katalogu services aktywną konfigurację usługi Orion. Zmień tylko retries na 4, zachowując pozostałe ustawienia. Po zmianie uruchom node checks/retry.cjs i podaj wynik kontroli. Nie zmieniaj pliku kontrolnego ani konfiguracji przykładowej.",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	lastSeq := -1
	for i, prompt := range prompts {
		trace, sid := chatPhaseProbeTurn(t, handler, ctx, prompt, report.SessionID, fmt.Sprintf("broad-operation-live-%s-%d", filepath.Base(run), i), false, true)
		report.SessionID = sid
		turn := broadOperationLiveTurn{Prompt: prompt, WallMS: trace.point("receipt_return") / int64(time.Millisecond), Trace: trace.result("local-qwen-gui", "broad-operations")}
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
		turn.IndependentChecksTogether, turn.VerificationAfterEdit, turn.ToolsAfterCompletion, turn.RepeatedSuccessfulCalls = broadOperationLiveEvidence(turn.Messages, i)
		if i == 0 {
			for _, value := range []string{"Marek Lis", "11"} {
				if !strings.Contains(turn.FinalText, value) {
					turn.Passed = false
				}
			}
			turn.Passed = turn.Passed && broadOperationLiveHasEvidence(turn.Messages, "OWNER_CHECK_OK") && broadOperationLiveHasEvidence(turn.Messages, "CAPACITY_CHECK_OK")
		}
		if i == 1 {
			b, err := os.ReadFile(filepath.Join(home, "services", "runtime", "live.json"))
			var actual map[string]any
			if err != nil || json.Unmarshal(b, &actual) != nil || actual["service"] != "Orion" || actual["retries"] != float64(4) || actual["timeout_ms"] != float64(1400) || actual["port"] != float64(8234) || len(actual) != 4 {
				turn.Passed = false
			}
			turn.Passed = turn.Passed && turn.VerificationAfterEdit
		}
		// Every unrelated source and checker must retain its original bytes.
		for name, expected := range fixtures {
			if i == 1 && name == "services/runtime/live.json" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
			if err != nil || string(b) != expected {
				turn.Passed = false
			}
		}
		report.Turns = append(report.Turns, turn)
		t.Logf("broad turn %d: %.3fs, %d model calls, %d tools, %d failed, together=%v, post-completion=%d", i+1, float64(turn.WallMS)/1000, turn.Summary.ModelCalls, turn.Summary.ToolCalls, turn.Summary.ToolFailures, turn.IndependentChecksTogether, turn.ToolsAfterCompletion)
		if !turn.Passed {
			t.Fatalf("broad operation turn %d failed; transcript in receipt", i+1)
		}
	}
}

func broadOperationLiveHasEvidence(messages []llm.Message, marker string) bool {
	calls := map[string]llm.ToolCall{}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			calls[call.ID] = call
		}
		if msg.Role == llm.RoleTool && broadOperationLiveCommandMarker(calls[msg.ToolCallID], msg, marker) {
			return true
		}
	}
	return false
}

func broadOperationLiveCommandMarker(call llm.ToolCall, msg llm.Message, marker string) bool {
	if call.Name != "ctx_execute" {
		return false
	}
	checker := map[string]string{"OWNER_CHECK_OK": "checks/owner.cjs", "CAPACITY_CHECK_OK": "checks/capacity.cjs", "RETRY_CHECK_OK": "checks/retry.cjs"}[marker]
	var args struct {
		Command []string `json:"command"`
	}
	if checker == "" || json.Unmarshal([]byte(call.Arguments), &args) != nil || !broadOperationLiveRunsChecker(args.Command, checker) {
		return false
	}
	var result struct {
		Stdout   string `json:"stdout"`
		ExitCode *int   `json:"exit_code"`
		Error    string `json:"error"`
	}
	return json.Unmarshal([]byte(msg.Content), &result) == nil && result.ExitCode != nil && *result.ExitCode == 0 && result.Error == "" && strings.Contains(result.Stdout, marker)
}

func broadOperationLiveRunsChecker(argv []string, checker string) bool {
	if len(argv) < 2 {
		return false
	}
	binary := strings.ToLower(filepath.Base(argv[0]))
	if binary == "node" || binary == "node.exe" {
		return strings.TrimPrefix(strings.ReplaceAll(argv[1], `\`, "/"), "./") == checker
	}
	// Complex shell/interpreter variants require offline transcript review.
	// Never infer execution from a string which could simply be echoed.
	return false
}

// Assess actual successful results, not a model's claim to have run a check.
// Two CLI checks count as grouped when returned from the same assistant tool
// round. The automatic verifier accepts explicit Node entry-script argv only;
// complex commands require separate offline transcript review.
func broadOperationLiveEvidence(messages []llm.Message, scenario int) (bool, bool, int, int) {
	callRounds := map[string]int{}
	callKeys := map[string]string{}
	calls := map[string]llm.ToolCall{}
	seen := map[string]bool{}
	round, ownerRound, capacityRound := -1, -1, -1
	editResult, verifiedResult, lastCompletion := -1, -1, -1
	repeated := 0
	for i, msg := range messages {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			round++
			for _, call := range msg.ToolCalls {
				callRounds[call.ID] = round
				calls[call.ID] = call
				var decoded any
				if json.Unmarshal([]byte(call.Arguments), &decoded) == nil {
					b, _ := json.Marshal(decoded)
					callKeys[call.ID] = call.Name + ":" + string(b)
				} else {
					callKeys[call.ID] = call.Name + ":" + call.Arguments
				}
			}
		}
		if msg.Role != llm.RoleTool {
			continue
		}
		call := calls[msg.ToolCallID]
		r := callRounds[msg.ToolCallID]
		lowerResult := strings.ToLower(strings.TrimSpace(msg.Content))
		failed := strings.HasPrefix(lowerResult, "error:") || strings.Contains(lowerResult, "command_failed exit=") || strings.Contains(lowerResult, "retry_check_failed")
		if call.Name == "ctx_execute" {
			var result struct {
				ExitCode *int   `json:"exit_code"`
				Error    string `json:"error"`
			}
			failed = failed || json.Unmarshal([]byte(msg.Content), &result) != nil || result.ExitCode == nil || *result.ExitCode != 0 || result.Error != ""
		}
		if !failed {
			key := callKeys[msg.ToolCallID]
			if key != "" {
				if seen[key] {
					repeated++
				}
				seen[key] = true
			}
		}
		if !failed && broadOperationLiveEditsConfig(call) {
			editResult = i
		}
		if broadOperationLiveCommandMarker(call, msg, "OWNER_CHECK_OK") {
			ownerRound = r
		}
		if broadOperationLiveCommandMarker(call, msg, "CAPACITY_CHECK_OK") {
			capacityRound = r
		}
		if broadOperationLiveCommandMarker(call, msg, "RETRY_CHECK_OK") {
			verifiedResult = i
			if lastCompletion < 0 {
				lastCompletion = i
			}
		}
		if scenario == 0 && lastCompletion < 0 && ownerRound >= 0 && capacityRound >= 0 {
			lastCompletion = i
		}
	}
	afterCalls := map[string]bool{}
	if lastCompletion >= 0 {
		for _, msg := range messages[lastCompletion+1:] {
			// Include ordered results from later members of the same batch,
			// while counting declarations and their result only once.
			if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
				afterCalls[msg.ToolCallID] = true
			}
			for _, call := range msg.ToolCalls {
				afterCalls[call.ID] = true
			}
		}
	}
	// Result chronology also accepts a scheduler-ordered edit and real check
	// in one assistant batch. It does not infer physical parallel dispatch.
	verified := verifiedResult >= 0 && (editResult < 0 || verifiedResult > editResult)
	return ownerRound >= 0 && ownerRound == capacityRound, verified, len(afterCalls), repeated
}

func broadOperationLiveEditsConfig(call llm.ToolCall) bool {
	if call.Name != "patch_file" && call.Name != "write_file" && call.Name != "create_file" {
		return false
	}
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) != nil {
		return false
	}
	return strings.TrimPrefix(strings.ReplaceAll(args.Path, `\`, "/"), "./") == "services/runtime/live.json"
}

func TestBroadOperationLiveEvidence(t *testing.T) {
	cli := func(id, checker, marker string) (llm.ToolCall, llm.Message) {
		return llm.ToolCall{ID: id, Name: "ctx_execute", Arguments: `{"command":["node","` + checker + `"]}`}, llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: `{"stdout":"` + marker + `","exit_code":0}`}
	}
	owner, ownerResult := cli("o", "checks/owner.cjs", "OWNER_CHECK_OK")
	capacity, capacityResult := cli("c", "checks/capacity.cjs", "CAPACITY_CHECK_OK")
	grouped := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{owner, capacity}}, ownerResult, capacityResult, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "extra", Name: "read_lines", Arguments: `{"file":"owner.json"}`}}}}
	together, _, after, _ := broadOperationLiveEvidence(grouped, 0)
	if !together || after != 1 {
		t.Fatalf("same response and post-completion count: together=%v after=%d", together, after)
	}
	edit := llm.ToolCall{ID: "edit", Name: "patch_file", Arguments: `{"path":"services/runtime/live.json"}`}
	check, checkResult := cli("verify", "checks/retry.cjs", "RETRY_CHECK_OK")
	ordered := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{edit, check}}, {Role: llm.RoleTool, ToolCallID: "edit", Content: "Patched services/runtime/live.json"}, checkResult}
	_, verified, _, _ := broadOperationLiveEvidence(ordered, 1)
	if !verified {
		t.Fatal("ordered edit and genuine check in one assistant round must be accepted")
	}
	ordered[1], ordered[2] = ordered[2], ordered[1]
	_, verified, _, _ = broadOperationLiveEvidence(ordered, 1)
	if verified {
		t.Fatal("check before edit must not prove completion")
	}
	fake := owner
	fake.Name = "read_lines"
	if broadOperationLiveCommandMarker(fake, ownerResult, "OWNER_CHECK_OK") {
		t.Fatal("source-code marker must not count as a CLI check")
	}
	fake = owner
	fake.Arguments = `{"command":["node","-e","console.log('OWNER_CHECK_OK')"]}`
	if broadOperationLiveCommandMarker(fake, ownerResult, "OWNER_CHECK_OK") {
		t.Fatal("a printed marker must not replace running the installed CLI checker")
	}
	missingExit := ownerResult
	missingExit.Content = `{"stdout":"OWNER_CHECK_OK"}`
	if broadOperationLiveCommandMarker(owner, missingExit, "OWNER_CHECK_OK") {
		t.Fatal("missing exit code must not prove successful check")
	}
	bad := owner
	bad.Arguments = `{"command":"node checks/owner.cjs"}`
	if broadOperationLiveCommandMarker(bad, ownerResult, "OWNER_CHECK_OK") {
		t.Fatal("bad argv shape must not prove a check")
	}
	third := llm.ToolCall{ID: "extra", Name: "read_lines", Arguments: `{"file":"owner.json"}`}
	laterResult := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{owner, capacity, third}}, ownerResult, capacityResult, {Role: llm.RoleTool, ToolCallID: "extra", Content: "owner=Marek Lis"}}
	_, _, after, _ = broadOperationLiveEvidence(laterResult, 0)
	if after != 1 {
		t.Fatalf("later same-batch result count = %d, want 1", after)
	}
	lowerFailure := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{third}}, {Role: llm.RoleTool, ToolCallID: "extra", Content: "error: missing file"}, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "extra2", Name: third.Name, Arguments: third.Arguments}}}, {Role: llm.RoleTool, ToolCallID: "extra2", Content: "error: missing file"}}
	_, _, _, repeated := broadOperationLiveEvidence(lowerFailure, 0)
	if repeated != 0 {
		t.Fatal("failed calls must not count as repeated successful work")
	}
	if broadOperationLiveRunsChecker([]string{"cmd", "/c", "echo node checks/capacity.cjs"}, "checks/capacity.cjs") {
		t.Fatal("an echoed shell invocation must not count")
	}
	if broadOperationLiveRunsChecker([]string{"node", "-e", "console.log('OWNER_CHECK_OK')", "checks/owner.cjs"}, "checks/owner.cjs") {
		t.Fatal("unused checker argument after inline Node must not count")
	}
	mention := llm.ToolCall{Name: "write_file", Arguments: `{"path":"notes.txt","content":"services/runtime/live.json"}`}
	if broadOperationLiveEditsConfig(mention) {
		t.Fatal("mentioning a config in unrelated content must not count as editing it")
	}
	if !broadOperationLiveEditsConfig(edit) {
		t.Fatal("schema path must identify the genuine edit target")
	}
}

// Apply one final validation implementation to both frozen model transcripts.
// This avoids rerunning a model merely because a receipt validator was fixed.
func TestBroadOperationLiveRevalidateReceipts(t *testing.T) {
	paths := filepath.SplitList(os.Getenv("SUPERCLI_BROAD_OPERATION_RECEIPTS"))
	if len(paths) == 0 {
		t.Skip("opt-in validation of completed live receipts")
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Run      string                   `json:"run"`
			Fixtures map[string]string        `json:"fixtures"`
			Turns    []broadOperationLiveTurn `json:"turns"`
			Passed   bool                     `json:"passed"`
		}
		if err := json.Unmarshal(b, &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Turns) != 2 || !report.Passed {
			t.Fatalf("receipt not complete/pass: %s", path)
		}
		for i := range report.Turns {
			turn := &report.Turns[i]
			turn.IndependentChecksTogether, turn.VerificationAfterEdit, turn.ToolsAfterCompletion, turn.RepeatedSuccessfulCalls = broadOperationLiveEvidence(turn.Messages, i)
			if i == 0 && (!broadOperationLiveHasEvidence(turn.Messages, "OWNER_CHECK_OK") || !broadOperationLiveHasEvidence(turn.Messages, "CAPACITY_CHECK_OK")) {
				t.Errorf("actual installed CLI checks missing: %s", path)
			}
			if i == 1 && !turn.VerificationAfterEdit {
				t.Errorf("actual installed check after state change missing: %s", path)
			}
		}
		for name, expected := range report.Fixtures {
			if name == "services/runtime/live.json" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(report.Run, "workspace", filepath.FromSlash(name)))
			if err != nil || string(b) != expected {
				t.Errorf("unrelated/checker fixture changed: %s in %s", name, path)
			}
		}
		b, err = os.ReadFile(filepath.Join(report.Run, "workspace", "services", "runtime", "live.json"))
		var actual map[string]any
		if err != nil || json.Unmarshal(b, &actual) != nil || actual["service"] != "Orion" || actual["retries"] != float64(4) || actual["timeout_ms"] != float64(1400) || actual["port"] != float64(8234) || len(actual) != 4 {
			t.Errorf("final state incorrect: %s", path)
		}
		b, err = json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(report.Run, "revalidated.json"), b, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("identical final validator: %s", path)
	}
}
