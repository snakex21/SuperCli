package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
)

// This is an opt-in QUALITY experiment, not a coding/performance benchmark.
// ALL historical users, assistant messages and tool/worker results below are
// SYNTHETIC fixtures. No command, edit, worker, application or export in those
// histories is executed. Only SummarizeForCompaction uses the real provider.
// The unmodified production prompt is used, with the same selected max effort
// as the paired coding experiment. No instruction is added just for evaluation.
//
// Environment: SUPERCLI_COMPACTION_QUALITY_URL/MODEL, optional LABEL and OUT.
// URL must be loopback HTTP. OUT must remain inside this repository's .tmp;
// otherwise a unique run is created under .tmp/compaction-instruction-quality.
// The two calls run sequentially and await completion, with no progress polling.
// Ordinary go test skips the live experiment; the offline evidence test below
// checks the validator's negative examples without contacting a model.
func TestCompactionInstructionQualityLive(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("SUPERCLI_COMPACTION_QUALITY_URL"))
	if base == "" {
		t.Skip("opt-in synthetic-history compaction quality experiment")
	}
	u, err := url.Parse(base)
	model := strings.TrimSpace(os.Getenv("SUPERCLI_COMPACTION_QUALITY_MODEL"))
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || model == "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		t.Fatal("explicit loopback HTTP URL and model are required")
	}
	parent, err := compactionQualityOutputParent(os.Getenv("SUPERCLI_COMPACTION_QUALITY_OUT"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	previousThinking, previousEffort := llm.ThinkingEnabled(), llm.ReasoningEffort()
	t.Cleanup(func() {
		llm.SetThinkingEnabled(previousThinking)
		_ = llm.SetReasoningEffort(previousEffort)
	})
	llm.SetThinkingEnabled(true)
	if err := llm.SetReasoningEffort("max"); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range compactionQualityFixtures() {
		t.Run(fixture.Name, func(t *testing.T) {
			report := compactionQualityReport{
				Label: os.Getenv("SUPERCLI_COMPACTION_QUALITY_LABEL"), Model: model,
				SyntheticHistory: true, RealSummarizerOnly: true, ManualReviewRequired: true,
				Fixture: fixture, Thinking: llm.ThinkingEnabled(), ReasoningEffort: llm.ReasoningEffort(),
				Instruction: compactionPrompt, InstructionSHA256: compactionQualitySHA([]byte(compactionPrompt)),
				HistorySHA256: compactionQualityJSONSHA(fixture.History), Transcript: RenderCompactTranscript(fixture.History),
				Limitations: []string{
					"Synthetic history: historical commands, edits, workers and exports did not run.",
					"Section/clause checks are bounded lexical evidence, not a general semantic judge; manually review contradictions and all expected facts in the full summary.",
					"Only the production summarizer is exercised: no CompactFacts appending, context split, main continuation, or actual resumption is tested.",
					"500 tokens is a prompt instruction; bytes and usage are recorded, not treated as an exact visible-summary tokenizer.",
					"TTFT includes transport/backend wait; cached usage absent is unknown, not proof of no cache. Wire max proves the requested control, not backend compliance.",
				},
			}
			report.TranscriptSHA256 = compactionQualitySHA([]byte(report.Transcript))
			transport := compactionQualityHTTPTransport()
			defer transport.CloseIdleConnections()
			wire := &compactionQualityTransport{inner: transport, host: u.Host, model: model, transcript: report.Transcript}
			observer := &compactionQualityObserver{}
			var statsMu sync.Mutex
			defer func() {
				observer.mu.Lock()
				report.CompleteCalls, report.RawAnswer, report.FinishReason = observer.calls, strings.TrimSpace(stripThinking(observer.content.String())), observer.finish
				report.ToolFragments, report.UsageReported = observer.toolFragments, observer.usageReported
				observer.mu.Unlock()
				wire.mu.Lock()
				report.WireRequests = append([]compactionQualityWireRequest(nil), wire.requests...)
				wire.mu.Unlock()
				statsMu.Lock()
				report.Passed = report.Passed && !t.Failed()
				data, writeErr := json.MarshalIndent(report, "", "  ")
				statsMu.Unlock()
				path := filepath.Join(run, fixture.Name+".json")
				if writeErr == nil {
					writeErr = os.WriteFile(path, data, 0600)
				}
				if writeErr != nil {
					t.Errorf("save quality receipt: %v", writeErr)
				}
				t.Logf("SYNTHETIC history; actual summarizer receipt: %s", path)
			}()
			caps := llm.NewCapabilityRegistry()
			caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, Stream: true, Source: llm.SourceProvider})
			provider, err := llm.NewOpenAI(llm.OpenAIConfig{
				BaseURL: base, Model: model, MaxTokens: 8192, Timeout: 180 * time.Second, Capabilities: caps,
				HTTPClient: &http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
			})
			if err != nil {
				report.Error = err.Error()
				t.Fatal(err)
			}
			observer.inner = provider
			metered := llm.Metered(observer, "openai-local-quality", llm.PurposeCompact, func(stat llm.CallStat) {
				statsMu.Lock()
				report.Calls = append(report.Calls, stat)
				statsMu.Unlock()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			started := time.Now()
			report.Summary, err = SummarizeForCompaction(ctx, metered, fixture.History)
			report.DurationMS = time.Since(started).Milliseconds()
			report.SummaryBytes = len(report.Summary)
			if err != nil {
				report.Error = err.Error()
				t.Fatal(err)
			}
			report.Checks = compactionQualityChecks(fixture.Name, report.Summary)
			observer.mu.Lock()
			complete := observer.calls == 1 && observer.finish == "stop" && observer.toolFragments == 0
			observer.mu.Unlock()
			report.Passed = complete && compactionQualityAllPassed(report.Checks)
			if !report.Passed {
				t.Fatalf("synthetic-history quality evidence failed: %v; full receipt retained", report.Checks)
			}
		})
	}
}

type compactionQualityFixture struct {
	Name     string        `json:"name"`
	Purpose  string        `json:"purpose"`
	Expected []string      `json:"expected_facts_for_manual_review"`
	History  []llm.Message `json:"synthetic_history"`
}

type compactionQualityReport struct {
	Label, Model, Instruction, InstructionSHA256, HistorySHA256, Transcript, TranscriptSHA256 string
	SyntheticHistory, RealSummarizerOnly, ManualReviewRequired, Thinking                      bool
	ReasoningEffort, Summary, RawAnswer, FinishReason, Error                                  string
	Fixture                                                                                   compactionQualityFixture
	SummaryBytes, CompleteCalls, ToolFragments                                                int
	UsageReported, Passed                                                                     bool
	DurationMS                                                                                int64
	Checks                                                                                    map[string]bool
	Calls                                                                                     []llm.CallStat
	WireRequests                                                                              []compactionQualityWireRequest
	Limitations                                                                               []string
}

func compactionQualityFixtures() []compactionQualityFixture {
	call := func(id, name, args string) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}}
	}
	result := func(id, name, text string) llm.Message {
		return llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: name, Content: text}
	}
	return []compactionQualityFixture{
		{
			Name:    "failure_unfinished_workers",
			Purpose: "Preserve open failure/running status, applicable user constraints, Unicode paths and the distinction between completed investigation and failed edit.",
			Expected: []string{
				"Tests at projekty/Łódź Demo/tests/discount.test.cjs must remain unchanged; no new packages, no publishing; application data stays next to executable.",
				"ReferenceError: discountRate is not defined remains open in projekty/Łódź Demo/src/discount.cjs; first test exited 1.",
				"worker-audit-17 completed its investigation: config.rate is the existing value to use; this investigation must not be restarted.",
				"worker-fix-09 failed/canceled before edits; no patch is completed merely because the assistant promised one.",
				"The second test node --test \"projekty/Łódź Demo/tests/discount.test.cjs\" is still running as command-check-42; no passing/final result exists.",
				"Next collect command-check-42 completion with read_output, then resume worker-fix-09 with send_message; do not start another test while this command runs.",
			},
			History: []llm.Message{
				{Role: llm.RoleUser, Content: "Fix the discount error in projekty/Łódź Demo/src/discount.cjs. Keep projekty/Łódź Demo/tests/discount.test.cjs unchanged. No new packages. Store all application data next to the executable. Do not publish. Check with node --test \"projekty/Łódź Demo/tests/discount.test.cjs\"."},
				call("test-first", "ctx_execute", `{"command":"node --test \"projekty/Łódź Demo/tests/discount.test.cjs\""}`),
				result("test-first", "ctx_execute", "command_failed exit=1\nReferenceError: discountRate is not defined at projekty/Łódź Demo/src/discount.cjs:8\n1 test failed; 0 passed."),
				call("audit-start", "spawn_agent", `{"task":"investigate discount failure"}`),
				result("audit-start", "spawn_agent", `{"worker_id":"worker-audit-17","status":"running"}`),
				call("fix-start", "spawn_agent", `{"task":"fix discount implementation without changing tests"}`),
				result("fix-start", "spawn_agent", `{"worker_id":"worker-fix-09","status":"running"}`),
				call("workers-wait", "wait_agent", `{"worker_ids":["worker-audit-17","worker-fix-09"]}`),
				result("workers-wait", "wait_agent", `{"workers":[{"worker_id":"worker-audit-17","status":"done","finding":"discountRate is undefined; config.rate is the existing configured value to use. Investigation complete; no edits."},{"worker_id":"worker-fix-09","status":"failed","error":"context canceled before patch","files_modified":[]}]}`),
				{Role: llm.RoleAssistant, Content: "I will patch the discount implementation and rerun the tests after the worker continues."},
				call("test-current", "ctx_execute", `{"command":"node --test \"projekty/Łódź Demo/tests/discount.test.cjs\""}`),
				result("test-current", "ctx_execute", `{"status":"running","session_id":"command-check-42","exit_code":null,"output":"Test started; no completion result yet."}`),
				{Role: llm.RoleUser, Content: "Next collect completion of command-check-42 with read_output before invoking another test. Then use send_message to resume worker-fix-09 on its original task. Do not repeat worker-audit-17's successful investigation. The promised patch has not been made."},
			},
		},
		{
			Name:    "revoked_constraint_independent_work",
			Purpose: "Apply a later user override while preserving unchanged constraints and separating a completed code fix from an independent failed application export.",
			Expected: []string{
				"The old keep-README-unchanged requirement is revoked: README.md may now be edited; its Quantity section update remains pending.",
				"Locale remains pl and remote uploading remains forbidden; do not force-close the editor to remove its lock.",
				"src/parser.cjs was patched so explicit quantity 0 is preserved; node --test tests/parser.test.cjs exited 0, 2/2 passed.",
				"Independent studio-render export to artefakty/Żółw Demo/preview.png failed with E_LOCKED, exit 2, and created no output file.",
				"Wait for the owner to close the exclusive editor, then retry the export; do not call it completed and do not redo the already tested parser correction.",
			},
			History: []llm.Message{
				{Role: llm.RoleUser, Content: "Two independent tasks: fix src/parser.cjs so explicit quantity 0 stays 0, and export a preview using studio-render to artefakty/Żółw Demo/preview.png. Keep README.md unchanged. Locale must remain pl. Do not upload anything remotely."},
				call("parser-patch", "patch_file", `{"path":"src/parser.cjs","old":"quantity || 1","new":"quantity === undefined ? 1 : quantity"}`),
				result("parser-patch", "patch_file", "Patched src/parser.cjs: replacements=1 changed=true"),
				call("parser-check", "ctx_execute", `{"command":"node --test tests/parser.test.cjs"}`),
				result("parser-check", "ctx_execute", `{"exit_code":0,"stdout":"2/2 tests passed: zero quantity is preserved; omitted quantity defaults to 1."}`),
				call("preview-export", "ctx_execute", `{"command":"studio-render --project \"projekty/Żółw Demo/scene.json\" --output \"artefakty/Żółw Demo/preview.png\""}`),
				result("preview-export", "ctx_execute", "command_failed exit=2\nE_LOCKED: project opened exclusively in editor; no output file written. Owner must close the exclusive editor before retry."),
				{Role: llm.RoleAssistant, Content: "The preview should be ready now. I will finish the README later."},
				{Role: llm.RoleUser, Content: "Change of requirement: revoke the old instruction to leave README.md unchanged. You may now edit README.md; next update its Quantity section to describe zero and the omitted default. The locale pl and no-remote-upload constraints remain. Wait for the owner to close the editor before retrying the independent export. Do not force-close it."},
			},
		},
	}
}

func compactionQualityChecks(name, summary string) map[string]bool {
	sections, valid := compactionQualitySections(summary)
	checks := map[string]bool{"four_nonempty_ordered_sections": valid, "not_truncated": !strings.Contains(summary, "[summary truncated]")}
	done, state, pending := sections["done"], sections["state"], sections["pending"]
	all := done + "\n" + state + "\n" + pending
	noEdits := `(?:no (?:patch|edits?|changes)|not (?:patched|edited|modified)|before (?:patch|edits?)|unmodified|no files (?:changed|modified)|nie (?:zmien|popraw))`
	if name == "failure_unfinished_workers" {
		checks["unicode_source_path"] = strings.Contains(all, "projekty/łódź demo/src/discount.cjs")
		checks["test_path_protected"] = compactionQualityClause(state, `projekty/łódź demo/tests/discount\.test\.cjs`, `(?:unchanged|do not (?:edit|change|modify)|don't (?:edit|change|modify)|read.only|nie (?:zmien|modyfik|rusz))`)
		checks["no_new_dependencies"] = compactionQualityClause(state, `(?:packages|dependencies|pakiet|zależno)`, `(?:no |without |do not add |don't add |bez |nie dodaw)`)
		checks["portable_application_data"] = compactionQualityClause(state, `(?:data|dane|danych)`, `(?:next to (?:the )?executable|application folder|folderze aplikacji|obok (?:pliku )?(?:wykonywalnego|exe))`)
		checks["publication_prohibited"] = compactionQualityClause(state, `(?:publish|publik)`, `(?:do not |don't |no |never |forbid|zakaz|nie |bez )`)
		checks["open_reference_error"] = strings.Contains(done+state, "referenceerror") && strings.Contains(done+state, "discountrate") && !compactionQualityUnnegatedAction(done, `(?:referenceerror|discount(?:rate)? error)`, `(?:fixed|resolved|napraw|rozwiąz)`)
		checks["successful_investigation_done"] = strings.Contains(done, "config.rate") && compactionQualityClause(done, `(?:investigation|investigat|worker.audit.17|analiz|badani)`, `(?:complete|done|found|finding|undefined|success|ustal|zakończ)`)
		checks["successful_investigation_not_pending"] = !compactionQualityUnnegatedAction(pending, `(?:investigation|worker.audit.17|analiz)`, `(?:repeat|restart|rerun|redo|ponow|powtórz)`)
		checks["failed_worker_preserved"] = compactionQualityClause(all, `worker.fix.09`, `(?:fail|cancel|błąd|anul|failed)`)
		checks["promise_not_completed_patch"] = regexp.MustCompile(noEdits).MatchString(all) && !compactionQualityUnnegatedAction(done, `(?:patch|implementation|discount\.cjs)`, `(?:completed|patched|fixed|changed=true|naprawiono)`)
		checks["running_command_not_passed"] = compactionQualityClause(all, `command.check.42`, `(?:running|still active|in progress|not complete|no (?:final|completion)|trwa|w toku|pending)`) && !compactionQualityUnnegatedAction(done, `(?:command.check.42|test)`, `(?:[1-9][0-9]*\s+(?:tests? )?passed|passed\s*[:=]?\s*[1-9]|(?:all )?tests? passed|exit(?:.code)?[ =:]+0|success|przechodz)`)
		checks["collect_then_resume_existing_worker"] = strings.Contains(pending, "read_output") && strings.Contains(pending, "command-check-42") && strings.Contains(pending, "send_message") && strings.Contains(pending, "worker-fix-09") && strings.Index(pending, "read_output") < strings.Index(pending, "send_message")
		checks["no_duplicate_running_test"] = compactionQualityClause(state+"\n"+pending, `(?:test|another|duplicate|ponow)`, `(?:before|do not (?:start|invoke|rerun|repeat)|don't (?:start|invoke)|no (?:new|another)|not (?:start|invoke)|przed|nie (?:uruch|wywoł))`)
	} else if name == "revoked_constraint_independent_work" {
		checks["parser_fix_and_zero_preserved_done"] = strings.Contains(done, "src/parser.cjs") && compactionQualityClause(done, `(?:quantity|iloś)`, `(?:0|zero)`)
		checks["parser_check_passed_done"] = compactionQualityClause(done, `(?:tests/parser\.test\.cjs|test)`, `(?:2\s*/\s*2[^;\n]{0,25}pass|2 (?:tests? )?passed|pass[^;\n]{0,25}2\s*/\s*2|exit(?:.code)?[ =:]+0)`)
		checks["readme_edit_currently_allowed"] = compactionQualityClause(state+"\n"+pending, `readme(?:\.md)?`, `(?:may (?:now )?(?:edit|be edited)|allow|permi|can (?:now )?(?:edit|be edited)|now (?:edit|update)|writable|revok|cofnię|można)`)
		checks["old_readme_prohibition_not_current"] = !compactionQualityCurrentProhibition(state+"\n"+pending, `readme(?:\.md)?`, `(?:do not (?:edit|change|modify)|don't (?:edit|change|modify)|must (?:remain|stay) unchanged|keep[^;\n]{0,40}unchanged|nie (?:zmien|modyfik|rusz))`)
		checks["readme_quantity_update_pending"] = compactionQualityReadmeUpdatePending(pending)
		checks["locale_pl_retained"] = compactionQualityClause(state, `(?:locale|language|język)`, `\bpl\b`)
		checks["remote_upload_prohibited"] = compactionQualityClause(state, `(?:upload|przesył|wysył)`, `(?:no |do not |don't |never |forbid|bez |nie |zakaz)`)
		checks["export_error_and_exact_unicode_path"] = strings.Contains(all, "e_locked") && strings.Contains(all, "artefakty/żółw demo/preview.png")
		checks["export_not_completed"] = !compactionQualityUnnegatedAction(done, `(?:preview|export|render)`, `(?:ready|complete|success|created|written|gotow|zapisano)`) && compactionQualityClause(all, `(?:export|output|preview|render)`, `(?:fail|not written|no (?:output|file)|pending|e_locked|failed|błąd|nie zapis)`)
		checks["wait_for_owner_then_retry_export"] = compactionQualityClause(pending, `(?:export|render|preview)`, `(?:retry|resume|ponow|wznów)`) && regexp.MustCompile(`(?:owner|exclusive|editor|lock|właściciel|edytor|blokad)`).MatchString(pending)
		checks["no_forced_editor_close"] = compactionQualityClause(state+"\n"+pending, `(?:force.?clos|forced clos|forcefully|forcibly|kill|wymus|zabij)`, `(?:do not|don't|no |never|not |nie |bez )`)
		checks["completed_parser_not_restarted"] = !compactionQualityUnnegatedAction(pending, `(?:parser|tests/parser\.test\.cjs)`, `(?:rerun|repeat|redo|(?:^\s*|\b(?:then|next|also)\s+)(?:fix|patch)\b|napraw|popraw|ponow)`)
	} else {
		checks["known_fixture"] = false
	}
	return checks
}

func compactionQualitySections(summary string) (map[string]string, bool) {
	re := regexp.MustCompile(`(?im)^\s*(Goal|Done|State|Pending):[ \t]*`)
	indices := re.FindAllStringSubmatchIndex(summary, -1)
	sections := map[string]string{}
	valid := len(indices) == 4
	for i, index := range indices {
		label := strings.ToLower(summary[index[2]:index[3]])
		end := len(summary)
		if i+1 < len(indices) {
			end = indices[i+1][0]
		}
		body := strings.TrimSpace(summary[index[1]:end])
		if i >= 4 || label != []string{"goal", "done", "state", "pending"}[min(i, 3)] || body == "" {
			valid = false
		}
		sections[label] = strings.ToLower(strings.ReplaceAll(body, `\`, "/"))
	}
	if len(indices) > 0 && strings.TrimSpace(summary[:indices[0][0]]) != "" {
		valid = false
	}
	return sections, valid
}

// A negation must occur in the same bounded clause as its subject. This avoids
// crediting "no publishing; add dependencies" as a prohibition on dependencies.
// It still cannot resolve arbitrary wording, pronouns, quotes or contradictions.
func compactionQualityClause(section, anchor, evidence string) bool {
	a, e := regexp.MustCompile(anchor), regexp.MustCompile(evidence)
	for _, clause := range regexp.MustCompile(`(?:[;\n]|[.!?]\s+)`).Split(section, -1) {
		if a.MatchString(clause) && e.MatchString(clause) {
			return true
		}
	}
	return false
}

func compactionQualityUnnegatedAction(section, anchor, action string) bool {
	a, e := regexp.MustCompile(anchor), regexp.MustCompile(action)
	negated := regexp.MustCompile(`(?:do not|don't|never|must not|no need to|no |not |nie trzeba|nie)[^;\n]{0,24}` + action)
	for _, clause := range regexp.MustCompile(`(?:[;\n]|[.!?]\s+)`).Split(section, -1) {
		if a.MatchString(clause) && e.MatchString(clause) && !negated.MatchString(clause) {
			return true
		}
	}
	return false
}

func compactionQualityCurrentProhibition(section, anchor, prohibition string) bool {
	a, e := regexp.MustCompile(anchor), regexp.MustCompile(prohibition)
	revoked := regexp.MustCompile(`(?:revok|supersed|no longer|cofnię|odwołan)`)
	for _, clause := range regexp.MustCompile(`(?:[;\n]|[.!?]\s+)`).Split(section, -1) {
		if a.MatchString(clause) && e.MatchString(clause) && !revoked.MatchString(clause) {
			return true
		}
	}
	return false
}

func compactionQualityReadmeUpdatePending(pending string) bool {
	const subject = `\breadme(?:\.md)?\b`
	const action = `(?:\b(?:update|edit|document|describe|add|write|finish)\b|aktualiz|opisz|udokument)`
	const completed = `\b(?:completed|done|finished|updated|documented|written)\b`
	for _, clause := range regexp.MustCompile(`(?:[;\n]|[.!?]\s+)`).Split(pending, -1) {
		if !regexp.MustCompile(subject).MatchString(clause) || !regexp.MustCompile(`\bquantity\b`).MatchString(clause) {
			continue
		}
		// "editor" in an unrelated export clause is not an edit instruction.
		// Likewise, a completed documentation outcome is not a pending update.
		if compactionQualityUnnegatedAction(clause, subject, action) && !compactionQualityUnnegatedAction(clause, subject, completed) {
			return true
		}
	}
	return false
}

func compactionQualityAllPassed(checks map[string]bool) bool {
	for _, passed := range checks {
		if !passed {
			return false
		}
	}
	return len(checks) > 0
}

type compactionQualityWireRequest struct {
	URL, SHA256 string
	Bytes       int
	Body        json.RawMessage
	Validation  compactionQualityWireValidation
	HTTPStatus  int
	HTTPError   string
}

type compactionQualityWireValidation struct {
	DecodedModel, ExpectedModel, DecodedEffort                         string
	DecodedMaxTokens, DecodedTools, DecodedMessages                    int
	SystemRole, UserRole                                               string
	SystemBytes, ExpectedSystemBytes, UserBytes, ExpectedUserBytes     int
	SystemSHA256, ExpectedSystemSHA256, UserSHA256, ExpectedUserSHA256 string
	Checks                                                             map[string]bool
}

type compactionQualityTransport struct {
	inner                   http.RoundTripper
	host, model, transcript string
	mu                      sync.Mutex
	requests                []compactionQualityWireRequest
}

func (p *compactionQualityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" || req.URL.Host != p.host {
		return nil, fmt.Errorf("quality experiment may contact only its configured loopback endpoint")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	validation, validationErr := compactionQualityValidateWireBody(body, p.model, compactionPrompt, p.transcript)
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, compactionQualityWireRequest{URL: req.URL.String(), SHA256: compactionQualitySHA(body), Bytes: len(body), Body: json.RawMessage(body), Validation: validation})
	p.mu.Unlock()
	if validationErr != nil {
		return nil, validationErr
	}
	response, err := p.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	var errorBody []byte
	if response.StatusCode/100 != 2 {
		errorBody, err = io.ReadAll(io.LimitReader(response.Body, 64*1024))
		_ = response.Body.Close()
		if err != nil {
			return nil, err
		}
		response.Body = io.NopCloser(bytes.NewReader(errorBody))
	}
	p.mu.Lock()
	p.requests[index].HTTPStatus = response.StatusCode
	p.requests[index].HTTPError = string(errorBody)
	p.mu.Unlock()
	return response, nil
}

// Pure validation is also usable on COMPLETED receipts without sending HTTP.
// All wire keys are explicit so the saved diagnostic reflects actual decoding,
// not a separately parsed PowerShell/object projection of the same JSON.
func compactionQualityValidateWireBody(body []byte, model, instruction, transcript string) (compactionQualityWireValidation, error) {
	diagnostic := compactionQualityWireValidation{ExpectedModel: model,
		ExpectedSystemBytes: len(instruction), ExpectedUserBytes: len(transcript),
		ExpectedSystemSHA256: compactionQualitySHA([]byte(instruction)), ExpectedUserSHA256: compactionQualitySHA([]byte(transcript)),
		Checks: map[string]bool{}}
	var payload struct {
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		Messages        []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools     []json.RawMessage `json:"tools"`
		MaxTokens int               `json:"max_tokens"`
	}
	// Explicit controls are checked on the ACTUAL wire body, including retries.
	if err := json.Unmarshal(body, &payload); err != nil {
		return diagnostic, fmt.Errorf("decode quality wire body: %w", err)
	}
	diagnostic.DecodedModel, diagnostic.DecodedEffort, diagnostic.DecodedMaxTokens = payload.Model, payload.ReasoningEffort, payload.MaxTokens
	diagnostic.DecodedTools, diagnostic.DecodedMessages = len(payload.Tools), len(payload.Messages)
	var systemText, userText string
	if len(payload.Messages) > 0 {
		diagnostic.SystemRole, systemText = payload.Messages[0].Role, payload.Messages[0].Content
	}
	if len(payload.Messages) > 1 {
		diagnostic.UserRole, userText = payload.Messages[1].Role, payload.Messages[1].Content
	}
	diagnostic.SystemBytes, diagnostic.UserBytes = len(systemText), len(userText)
	diagnostic.SystemSHA256, diagnostic.UserSHA256 = compactionQualitySHA([]byte(systemText)), compactionQualitySHA([]byte(userText))
	diagnostic.Checks = map[string]bool{
		"model":                payload.Model == model,
		"reasoning_effort_max": payload.ReasoningEffort == "max",
		"max_tokens_8192":      payload.MaxTokens == 8192,
		"no_tools":             len(payload.Tools) == 0,
		"two_messages":         len(payload.Messages) == 2,
		"system_role":          diagnostic.SystemRole == "system",
		"system_content_exact": systemText == instruction,
		"user_role":            diagnostic.UserRole == "user",
		"user_content_exact":   userText == transcript,
	}
	var mismatches []string
	for name, passed := range diagnostic.Checks {
		if !passed {
			mismatches = append(mismatches, name)
		}
	}
	if len(mismatches) != 0 {
		sort.Strings(mismatches)
		return diagnostic, fmt.Errorf("quality wire mismatches [%s]: model=%q want=%q effort=%q max_tokens=%d tools=%d messages=%d roles=%q/%q system_bytes=%d/%d user_bytes=%d/%d system_sha=%s/%s user_sha=%s/%s", strings.Join(mismatches, ","), payload.Model, model, payload.ReasoningEffort, payload.MaxTokens, len(payload.Tools), len(payload.Messages), diagnostic.SystemRole, diagnostic.UserRole, diagnostic.SystemBytes, diagnostic.ExpectedSystemBytes, diagnostic.UserBytes, diagnostic.ExpectedUserBytes, diagnostic.SystemSHA256, diagnostic.ExpectedSystemSHA256, diagnostic.UserSHA256, diagnostic.ExpectedUserSHA256)
	}
	return diagnostic, nil
}

func compactionQualityHTTPTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	return transport
}

type compactionQualityObserver struct {
	inner         llm.Provider
	mu            sync.Mutex
	calls         int
	content       strings.Builder
	finish        string
	toolFragments int
	usageReported bool
}

func (p *compactionQualityObserver) Name() string { return p.inner.Name() }

func (p *compactionQualityObserver) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if len(tools) != 0 {
		return nil, fmt.Errorf("quality summarizer must not offer tools")
	}
	in, err := p.inner.Complete(ctx, messages, tools)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.Delta, 32)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case delta, ok := <-in:
				if !ok {
					return
				}
				p.mu.Lock()
				p.content.WriteString(delta.Content)
				if delta.FinishReason != "" {
					p.finish = delta.FinishReason
				}
				if delta.ToolCall != nil {
					p.toolFragments++
				}
				p.usageReported = p.usageReported || delta.Usage != nil
				p.mu.Unlock()
				select {
				case out <- delta:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func compactionQualityOutputParent(out string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	repo := cwd
	for {
		if data, readErr := os.ReadFile(filepath.Join(repo, "go.mod")); readErr == nil && strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0]) == "module supercli" {
			break
		}
		parent := filepath.Dir(repo)
		if parent == repo {
			return "", fmt.Errorf("run the opt-in test from the SuperCli repository")
		}
		repo = parent
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return "", err
	}
	if out == "" {
		out = filepath.Join(repo, ".tmp", "compaction-instruction-quality")
	}
	if !filepath.IsAbs(out) {
		return "", fmt.Errorf("artifact directory must be absolute and repo-local")
	}
	out = filepath.Clean(out)
	// Resolve existing parents before creating anything, so an existing junction
	// cannot redirect portable test artifacts to a profile/system directory.
	existing := out
	for {
		_, statErr := os.Lstat(existing)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) || filepath.Dir(existing) == existing {
			return "", fmt.Errorf("resolve artifact directory: %w", statErr)
		}
		existing = filepath.Dir(existing)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	suffix, err := filepath.Rel(existing, out)
	if err != nil {
		return "", err
	}
	resolved = filepath.Join(resolved, suffix)
	rel, err := filepath.Rel(filepath.Join(repo, ".tmp"), resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact directory must resolve inside repository .tmp")
	}
	return resolved, nil
}

func compactionQualitySHA(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func compactionQualityJSONSHA(value any) string {
	body, _ := json.Marshal(value)
	return compactionQualitySHA(body)
}

func TestCompactionInstructionQualityEvidence(t *testing.T) {
	validFirst := "Goal: Fix discount error.\nDone: Investigation completed: config.rate is the configured value; worker-audit-17 found discountRate undefined.\nState: projekty/Łódź Demo/src/discount.cjs has open ReferenceError: discountRate is not defined; projekty/Łódź Demo/tests/discount.test.cjs must remain unchanged; no new packages; data next to executable; do not publish; worker-fix-09 failed before patch, no edits; command-check-42 still running, no final result.\nPending: read_output command-check-42 before another test; then send_message worker-fix-09 to resume."
	validSecond := "Goal: Fix parser and export preview.\nDone: Patched src/parser.cjs to preserve quantity 0; node --test tests/parser.test.cjs exited 0, 2/2 passed.\nState: README.md edit now allowed; locale pl; no remote upload; export artefakty/Żółw Demo/preview.png failed E_LOCKED, no output; do not force-close editor.\nPending: Update README.md Quantity section; retry export after owner closes exclusive editor."
	for name, summary := range map[string]string{"failure_unfinished_workers": validFirst, "revoked_constraint_independent_work": validSecond} {
		if checks := compactionQualityChecks(name, summary); !compactionQualityAllPassed(checks) {
			t.Fatalf("complete synthetic evidence rejected for %s: %v", name, checks)
		}
	}
	// Negative descriptions of an action are preserved state, not instructions
	// to repeat it; a failed attempt may also be reported in Done as an outcome.
	firstWithNegatives := strings.Replace(validFirst, "Done: ", "Done: First test failed, 0 passed; no patch completed; ", 1) + "; do not repeat worker-audit-17 investigation."
	secondWithFailedOutcome := strings.Replace(validSecond, "Done: ", "Done: Export failed E_LOCKED, no output file written; ", 1) + "; do not rerun tests/parser.test.cjs."
	for name, summary := range map[string]string{"failure_unfinished_workers": firstWithNegatives, "revoked_constraint_independent_work": secondWithFailedOutcome} {
		if checks := compactionQualityChecks(name, summary); !compactionQualityAllPassed(checks) {
			t.Fatalf("negative descriptions of completed/pending actions misread for %s: %v", name, checks)
		}
	}
	negative := []struct{ name, summary, check string }{
		{"failure_unfinished_workers", strings.Replace(validFirst, "no new packages", "new packages allowed", 1), "no_new_dependencies"},
		{"failure_unfinished_workers", strings.Replace(validFirst, "worker-fix-09 failed", "worker-new-10 failed", 1), "failed_worker_preserved"},
		{"failure_unfinished_workers", strings.Replace(validFirst, "Investigation completed", "Patched implementation completed; investigation completed", 1), "promise_not_completed_patch"},
		{"failure_unfinished_workers", strings.Replace(validFirst, "Done: ", "Done: Tests passed, exit_code=0; ", 1), "running_command_not_passed"},
		{"revoked_constraint_independent_work", strings.Replace(validSecond, "README.md edit now allowed", "README.md must remain unchanged", 1), "old_readme_prohibition_not_current"},
		{"revoked_constraint_independent_work", strings.Replace(validSecond, "Done: ", "Done: Preview export completed successfully; ", 1), "export_not_completed"},
		{"revoked_constraint_independent_work", strings.Replace(validSecond, "Update README.md Quantity section", "README.md Quantity section already completed", 1), "readme_quantity_update_pending"},
		{"revoked_constraint_independent_work", strings.Replace(validSecond, "Update README.md Quantity section", "README.md Quantity section mentioned; edit export settings", 1), "readme_quantity_update_pending"},
		{"revoked_constraint_independent_work", strings.Replace(validSecond, "Update README.md Quantity section", "README.md Quantity update already completed", 1), "readme_quantity_update_pending"},
		{"revoked_constraint_independent_work", strings.Replace(validSecond, "Update README.md Quantity section", "Do not update README.md Quantity section", 1), "readme_quantity_update_pending"},
	}
	for _, test := range negative {
		if compactionQualityChecks(test.name, test.summary)[test.check] {
			t.Errorf("contradictory/missing evidence incorrectly passed %s", test.check)
		}
	}
}

func TestCompactionInstructionQualityWireEvidence(t *testing.T) {
	const model = "synthetic-quality-wire"
	const transcript = "[user] projekty/Łódź Demo/src/discount.cjs\n"
	payload := map[string]any{
		"model": model, "reasoning_effort": "max", "max_tokens": 8192,
		"messages": []map[string]string{{"role": "system", "content": compactionPrompt}, {"role": "user", "content": transcript}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic, err := compactionQualityValidateWireBody(body, model, compactionPrompt, transcript); err != nil || !compactionQualityAllPassed(diagnostic.Checks) {
		t.Fatalf("valid actual-format JSON rejected without HTTP: %v, %+v", err, diagnostic)
	}
	payload["reasoning_effort"] = "low"
	body, _ = json.Marshal(payload)
	if diagnostic, err := compactionQualityValidateWireBody(body, model, compactionPrompt, transcript); err == nil || diagnostic.Checks["reasoning_effort_max"] {
		t.Fatal("changed effort must fail before HTTP")
	}
	// Optional replay of already completed local receipts: no provider, no HTTP,
	// no generation, and no rewriting of original evidence or output files.
	for _, path := range filepath.SplitList(os.Getenv("SUPERCLI_COMPACTION_QUALITY_WIRE_RECEIPTS")) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report compactionQualityReport
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		if len(report.WireRequests) == 0 {
			t.Fatalf("completed receipt has no recorded wire body: %s", path)
		}
		for i, request := range report.WireRequests {
			diagnostic, err := compactionQualityValidateWireBody(request.Body, report.Model, report.Instruction, report.Transcript)
			if err != nil {
				if i == 0 {
					t.Fatalf("completed receipt %s initial request fails pure validation: %v", path, err)
				}
				// The old failed run also recorded a compatibility retry with
				// effort omitted. It must remain rejected, not be forwarded by
				// this harness or treated as same-setting model evidence.
				t.Logf("completed receipt %s retry %d rejected; no HTTP: %v", path, i, err)
				continue
			}
			t.Logf("completed receipt %s request %d: parsed actual fields %+v; no HTTP", path, i, diagnostic)
		}
	}
}
