package webgui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

const codingCompactContinuationPrompt = "Na razie niczego nie wykonuj i nie używaj narzędzi. Na podstawie zachowanej historii krótko przypomnij przyczynę naprawionego błędu, wynik testów przed i po poprawce, pozostały następny krok oraz ograniczenia, których masz przestrzegać."

type codingCompactFixture struct {
	SourceReceipt string        `json:"source_receipt"`
	Home          string        `json:"home"`
	History       []llm.Message `json:"history"`
	Continuation  string        `json:"continuation"`
}

// The transcript's coding actions and test outputs come from the completed
// real GUI run. The long brief is explicitly user-provided fixture context,
// never an invented tool result. Both labels load the exact saved JSON bytes.
func TestAgentCodingCompactionPrepareFixture(t *testing.T) {
	source := strings.TrimSpace(os.Getenv("SUPERCLI_CODING_COMPACTION_SOURCE"))
	if source == "" {
		t.Skip("opt-in preparation from a completed real coding receipt")
	}
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var receipt codingLiveReport
	if err := json.Unmarshal(b, &receipt); err != nil || !receipt.Passed || !receipt.Evidence.FailedBeforeEdit || !receipt.Evidence.PassedAfterEdit {
		t.Fatalf("requires independently validated actual red-edit-green coding receipt: %v", err)
	}
	brief := "Zasady dalszej pracy w tym projekcie: wszystkie dane aplikacji mają pozostawać przenośne w folderze aplikacji. Nie dodawaj pakietów ani zewnętrznych zależności. Nie zmieniaj istniejących testów tests/invoice.test.cjs ani cennika src/catalog.cjs. Napraw tylko implementację naliczania faktur. Po jej naprawie i przejściu testów nadal pozostanie osobny krok: uzupełnić docs/API.md o kontrakt ilości (quantity). Ilość 0 jest poprawna i ma koszt 0; pominięta ilość domyślnie wynosi 1, a ilości ujemne i ułamkowe są odrzucane. Dokumentacja jeszcze nie została napisana. Niczego nie publikuj. Poniższe notatki to kontekst projektu, a nie lista dodatkowych zadań.\n\n"
	for i := 1; i <= 96; i++ {
		brief += fmt.Sprintf("Notatka utrzymaniowa %03d: faktura składa się z pozycji katalogowych, kwoty są całkowitymi groszami, moduły oddzielają agregację, ceny i dane. Zachowuj czytelne nazwy oraz mały zakres zmian.\n", i)
	}
	fixture := codingCompactFixture{SourceReceipt: source, Home: filepath.Join(receipt.Run, "workspace"), Continuation: codingCompactContinuationPrompt}
	fixture.History = append(fixture.History, llm.Message{Role: llm.RoleUser, Content: brief})
	fixture.History = append(fixture.History, receipt.Messages...)
	// A short preserved last-turn tail must not supply the facts the summary
	// is being tested for. The pending document requirement appears above.
	fixture.History = append(fixture.History, llm.Message{Role: llm.RoleUser, Content: "Dobrze. Wrócimy do pozostałego kroku po kompakcji."})
	fixture.History = append(fixture.History, llm.Message{Role: llm.RoleUser, Content: "Zachowaj stan. Po kompakcji przypomnimy plan dalszej pracy."})
	b, err = json.MarshalIndent(fixture, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(source))), "compaction-fixture.json"), b, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixed compaction fixture: %d bytes, %d messages", len(b), len(fixture.History))
}

type codingCompactLiveReport struct {
	Label             string                    `json:"label"`
	Model             string                    `json:"model"`
	Run               string                    `json:"run"`
	FixtureSHA256     string                    `json:"fixture_sha256"`
	Thinking          bool                      `json:"thinking"`
	ReasoningEffort   string                    `json:"reasoning_effort"`
	History           []llm.Message             `json:"history"`
	Continuation      string                    `json:"continuation"`
	Before            agent.ContextReport       `json:"before"`
	After             agent.ContextReport       `json:"after"`
	CompactEvent      agent.AutoCompactEvent    `json:"compact_event"`
	Summary           string                    `json:"summary"`
	SummaryBytes      int                       `json:"summary_bytes"`
	SummaryQuality    map[string]bool           `json:"summary_quality"`
	ContinuationText  string                    `json:"continuation_text"`
	ContinuationCheck map[string]bool           `json:"continuation_quality"`
	CompactMS         int64                     `json:"compact_ms"`
	ContinueMS        int64                     `json:"continue_ms"`
	TotalMS           int64                     `json:"total_ms"`
	Requests          []downloadLiveRequest     `json:"requests"`
	RequestTimings    []codingLiveRequestTiming `json:"request_timings"`
	RequestMessages   [][]llm.Message           `json:"request_messages"`
	Trace             chatPhaseProbeResult      `json:"trace"`
	ToolCalls         int                       `json:"tool_calls"`
	Errors            []string                  `json:"errors,omitempty"`
	Passed            bool                      `json:"passed"`
}

// Actual GUI loop compaction and its ordinary next main request. The model,
// selected effort, original history and retained tail stay fixed. The wrapper
// observes output; a tool fragment cancels this explicit no-operation task
// before a tool can execute, rather than fabricating a tool result.
func TestAgentCodingCompactionLive(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_CODING_COMPACTION_URL"))
	if baseURL == "" {
		t.Skip("opt-in local-model coding compaction evaluation")
	}
	u, err := url.Parse(baseURL)
	model := strings.TrimSpace(os.Getenv("SUPERCLI_CODING_COMPACTION_MODEL"))
	if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.RawQuery != "" || u.Fragment != "" || model == "" {
		t.Fatal("requires explicit loopback model URL and model name")
	}
	fixturePath := strings.TrimSpace(os.Getenv("SUPERCLI_CODING_COMPACTION_FIXTURE"))
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture codingCompactFixture
	if err := json.Unmarshal(b, &fixture); err != nil || len(fixture.History) < 10 || fixture.Continuation != codingCompactContinuationPrompt {
		t.Fatalf("invalid frozen coding compaction fixture: %v", err)
	}
	digest := sha256.Sum256(b)
	parent := filepath.Join(filepath.Dir(fixturePath), "compaction-runs")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	// Fixed data path keeps the GUI system prompt byte-identical. This task
	// never writes session history or changes the source workspace.
	data := filepath.Join(filepath.Dir(fixturePath), "compaction-data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
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
	eng, err := NewEngine(cfg, fixture.Home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	provider := &codingLiveProvider{base: downloadLiveProvider{inner: eng.prov}, captureMessages: true, validateRequest: codingCompactValidateRequest}
	eng.prov = provider
	loop, err := eng.newLoopWithSession(fixture.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := codingCompactLiveReport{Label: os.Getenv("SUPERCLI_CODING_COMPACTION_LABEL"), Model: model, Run: run, FixtureSHA256: hex.EncodeToString(digest[:]), Thinking: true, ReasoningEffort: "max", History: fixture.History, Continuation: fixture.Continuation}
	trace := &chatPhaseProbeTrace{start: time.Now(), points: map[string]int64{}, sseOutput: make(chan struct{}), finished: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, chatPhaseProbeTraceKey{}, trace)
	ctx = llm.WithCallSink(ctx, trace.recordCall)
	ctx = httptrace.WithClientTrace(ctx, chatPhaseProbeHTTPTrace(trace))
	defer func() {
		provider.base.mu.Lock()
		report.Requests = append([]downloadLiveRequest(nil), provider.base.requests...)
		provider.base.mu.Unlock()
		provider.mu.Lock()
		report.RequestTimings = append([]codingLiveRequestTiming(nil), provider.timings...)
		report.RequestMessages = append([][]llm.Message(nil), provider.requestMessages...)
		provider.mu.Unlock()
		report.Trace = trace.result("actual-gui-loop", "coding-compaction")
		report.Passed = report.Passed && !t.Failed()
		b, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(run, "receipt.json"), b, 0600)
		}
		if err != nil {
			t.Errorf("save compaction receipt: %v", err)
		}
		t.Logf("compaction receipt: %s", filepath.Join(run, "receipt.json"))
	}()
	report.Before = loop.ContextReport()
	totalStart := time.Now()
	compactStart := time.Now()
	report.CompactEvent, err = loop.CompactNow(ctx)
	report.CompactMS = time.Since(compactStart).Milliseconds()
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		t.Fatal(err)
	}
	report.After = loop.ContextReport()
	for _, message := range loop.Messages {
		if agent.IsLegacyCompactionSummary(message) {
			report.Summary = message.Content
			break
		}
	}
	report.SummaryBytes = len(report.Summary)
	report.SummaryQuality = codingCompactQuality(report.Summary)
	mainCtx, cancelMain := context.WithCancel(ctx)
	defer cancelMain()
	provider.mu.Lock()
	provider.cancelUnexpectedTools = cancelMain
	provider.mu.Unlock()
	continuedStart := time.Now()
	events, err := loop.Run(mainCtx, fixture.Continuation)
	if err != nil {
		t.Fatal(err)
	}
	done := false
	var continued strings.Builder
	for event := range events {
		switch event := event.(type) {
		case agent.MessageEvent:
			continued.WriteString(event.Text)
		case agent.ToolCallEvent:
			report.ToolCalls++
		case agent.ErrorEvent:
			report.Errors = append(report.Errors, event.Err.Error())
		case agent.DoneEvent:
			done = true
		}
	}
	report.ContinueMS = time.Since(continuedStart).Milliseconds()
	report.TotalMS = time.Since(totalStart).Milliseconds()
	report.ContinuationText = strings.TrimSpace(regexp.MustCompile(`(?s)<(?:thinking|think)>.*?</(?:thinking|think)>`).ReplaceAllString(continued.String(), ""))
	report.ContinuationCheck = codingCompactQuality(report.ContinuationText)
	provider.mu.Lock()
	report.ToolCalls += provider.unexpectedToolFragments
	provider.mu.Unlock()
	report.Passed = done && report.ToolCalls == 0 && len(report.Errors) == 0 && report.After.RawRequestTokens < report.Before.RawRequestTokens && codingCompactQualityPassed(report.SummaryQuality) && codingCompactQualityPassed(report.ContinuationCheck)
	t.Logf("compaction: compact=%.3fs main=%.3fs total=%.3fs, summary=%d bytes, context estimate=%d -> %d, quality=%v/%v", float64(report.CompactMS)/1000, float64(report.ContinueMS)/1000, float64(report.TotalMS)/1000, report.SummaryBytes, report.Before.RawRequestTokens, report.After.RawRequestTokens, report.SummaryQuality, report.ContinuationCheck)
	if !report.Passed {
		t.Fatal("compaction or continuation quality failed; durable full receipt retained")
	}
}

// Validate the real outgoing projection before spending a model call. The
// completed coding turn must be in the summarizer input and absent from the
// retained raw main tail, otherwise continuation would not test the summary.
func codingCompactValidateRequest(ctx context.Context, messages []llm.Message, _ []llm.ToolDef) error {
	if llm.PurposeFromContext(ctx) == llm.PurposeCompact {
		var transcript strings.Builder
		for _, message := range messages {
			if message.Role == llm.RoleUser {
				transcript.WriteString(message.Content)
			}
		}
		text := transcript.String()
		if strings.Count(text, "[tool call: ctx_execute ") < 2 || !strings.Contains(text, "[tool call: patch_file ") || !strings.Contains(text, "command_failed exit=1") || !strings.Contains(text, `"exit_code":0`) || !strings.Contains(text, "src/pricing.cjs") {
			return fmt.Errorf("fixture boundary excludes completed red-edit-green transcript from the actual summary request")
		}
		return nil
	}
	for _, message := range messages {
		if message.Role == llm.RoleSystem || agent.IsLegacyCompactionSummary(message) {
			continue
		}
		if message.Role == llm.RoleTool || message.Role == llm.RoleAssistant || len(message.ToolCalls) != 0 {
			return fmt.Errorf("actual coding transcript leaked into the main raw tail instead of the summary")
		}
	}
	return nil
}

func codingCompactQuality(text string) map[string]bool {
	lower := strings.ToLower(text)
	unchanged := strings.Contains(lower, "unchanged") || strings.Contains(lower, "untouched") || strings.Contains(lower, "bez zmian") || strings.Contains(lower, "nietknię") || strings.Contains(lower, "nie zmien") || strings.Contains(lower, "nie rusz") || strings.Contains(lower, "nie modyfik") || strings.Contains(lower, "do not change") || strings.Contains(lower, "do not modify") || strings.Contains(lower, "don't modify") || strings.Contains(lower, "no changes") || strings.Contains(lower, "preserve")
	sevenPassed := regexp.MustCompile(`(?i)(?:\b7\s*/\s*7\b|\b7\s+(?:test|pass)|\b(?:tests?|testów|pass(?:ed)?)\s*[:=]?\s*7\b)`).MatchString(text)
	red := regexp.MustCompile(`(?i)(?:\b2\s+(?:fail|test|błęd|poraż|nie przesz)|\b(?:fail(?:ed|ures)?|testów)\s*[:=]?\s*2\b|\b300\s*!==?\s*0\b|\bbefore\b.*\bfail|\bprzed\b.*(?:nie przech|fail|błęd))`).MatchString(text)
	noPublish := regexp.MustCompile(`(?i)(?:nie\s+publik|bez\s+publik|zakaz\s+publik|do\s+not\s+publish|don't\s+publish|no\s+publish|without\s+publishing)`).MatchString(text)
	return map[string]bool{
		"pricing_path":          strings.Contains(lower, "src/pricing.cjs"),
		"pending_document_path": strings.Contains(lower, "docs/api.md"),
		"quantity_zero_default": (strings.Contains(lower, "quantity") || strings.Contains(lower, "iloś")) && strings.Contains(lower, "0") && strings.Contains(lower, "1"),
		"observed_red_to_green": red && sevenPassed,
		"tests_protected":       (strings.Contains(lower, "test") || strings.Contains(lower, "tests/invoice.test.cjs")) && unchanged,
		"catalog_protected":     (strings.Contains(lower, "catalog") || strings.Contains(lower, "cennik")) && unchanged,
		"no_dependencies":       (strings.Contains(lower, "zależnoś") || strings.Contains(lower, "dependenc") || strings.Contains(lower, "pakiet") || strings.Contains(lower, "packages")) && (strings.Contains(lower, "no ") || strings.Contains(lower, "nie ") || strings.Contains(lower, "bez ") || strings.Contains(lower, "without")),
		"portable_data":         strings.Contains(lower, "przenoś") || strings.Contains(lower, "portable") || strings.Contains(lower, "folderze aplikacji") || strings.Contains(lower, "katalogu aplikacji") || strings.Contains(lower, "application folder"),
		"do_not_publish":        noPublish,
	}
}

func TestCodingCompactQualityEvidence(t *testing.T) {
	valid := "src/pricing.cjs fixed quantity 0/default 1, 2 failed before and 7 passed after. Tests and catalog unchanged. No new packages. Portable application folder. Pending docs/API.md; do not publish."
	if !codingCompactQualityPassed(codingCompactQuality(valid)) {
		t.Fatal("complete applicable constraints and real red-to-green facts must pass")
	}
	retrospective := strings.Replace(valid, "do not publish", "nothing has been published", 1)
	if codingCompactQuality(retrospective)["do_not_publish"] {
		t.Fatal("retrospective publication status must not replace a future user prohibition")
	}
	polishRed := strings.Replace(valid, "2 failed before and 7 passed after", "Przed: 2 nie przeszło. Po: 7 testów przechodzi", 1)
	if !codingCompactQuality(polishRed)["observed_red_to_green"] {
		t.Fatal("actual Polish red-to-green evidence must be accepted")
	}
	polishFailure := strings.Replace(valid, "2 failed before and 7 passed after", "Przed: 5/7 (2 porażki). Po: 7/7", 1)
	polishFailure = strings.Replace(polishFailure, "Tests and catalog unchanged", "Nie ruszać testów i cennika", 1)
	if !codingCompactQualityPassed(codingCompactQuality(polishFailure)) {
		t.Fatal("ordinary Polish failure and unchanged-contract wording must be accepted")
	}
}

func TestCodingCompactionLiveRevalidate(t *testing.T) {
	paths := filepath.SplitList(os.Getenv("SUPERCLI_CODING_COMPACTION_RECEIPTS"))
	if len(paths) == 0 {
		t.Skip("opt-in consistent validation of completed compaction receipts")
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report codingCompactLiveReport
		if err := json.Unmarshal(b, &report); err != nil {
			t.Fatal(err)
		}
		report.SummaryQuality = codingCompactQuality(report.Summary)
		report.ContinuationCheck = codingCompactQuality(report.ContinuationText)
		report.Passed = len(report.Errors) == 0 && report.ToolCalls == 0 && report.After.RawRequestTokens < report.Before.RawRequestTokens && codingCompactQualityPassed(report.SummaryQuality) && codingCompactQualityPassed(report.ContinuationCheck)
		b, err = json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(filepath.Dir(path), "receipt-revalidated.json"), b, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("identical final compaction validator: %s, overall quality=%v", path, report.Passed)
	}
}

func codingCompactQualityPassed(checks map[string]bool) bool {
	for _, passed := range checks {
		if !passed {
			return false
		}
	}
	return len(checks) > 0
}
