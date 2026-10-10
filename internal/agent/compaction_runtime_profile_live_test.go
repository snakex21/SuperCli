package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

// Opt-in diagnostic, not a latency A/B or a synthetic execution history. It
// loads the real portable default profile read-only, discovers native metadata,
// then sends the production compactor the summary from a completed receipt.
// Thinking, effort, sampling and generation limits follow ordinary startup.
// The injected transport observes the actual wire and permits ONE HTTP request:
// compatibility retries cannot silently remove/change the selected control.
// No tools, extra system instructions, config writes or alternate model calls.
// Set SUPERCLI_COMPACTION_RUNTIME_DATA/RECEIPT/OUT (OUT inside repo/.tmp) and
// EXPECTED_EFFORT to the independently inspected RESOLVED effort. Global and
// workspace layers are both read; a mismatch fails before native discovery or
// inference. This must not silently substitute a global default for a project.
// INPUT=prefix reuses the exact historical helper prefix from the receipt;
// default INPUT=summary measures another compaction of its completed summary.
// VALIDATE_ONLY=1 stops after read-only effort/input checks, before metadata
// discovery or any inference. This diagnoses archived projection differences.
func TestCompactionRuntimeProfileLive(t *testing.T) {
	dataDir := os.Getenv("SUPERCLI_COMPACTION_RUNTIME_DATA")
	if dataDir == "" {
		t.Skip("opt-in read-only runtime-profile compaction diagnostic")
	}
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	globalPath, projectPath := config.FindTomlPaths(dataDir, workspace)
	global, err := config.LoadToml(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := config.LoadToml(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	tc, err := config.ResolveConfig(dataDir, workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	// Mirror CLI startup's TOML-to-env step without changing the caller's
	// environment after the test. None of these values are written to disk.
	for _, key := range []string{"SUPERCLI_LLM_MODEL", "SUPERCLI_LLM_PROVIDER", "SUPERCLI_LLM_BASE_URL", "SUPERCLI_LLM_API_KEY", "SUPERCLI_DEBUG", "SUPERCLI_ALLOW_ALL"} {
		key, value := key, os.Getenv(key)
		_, present := os.LookupEnv(key)
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	config.TomlConfigToEnv(tc)
	cfg, err := config.Load(config.FlagOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyTomlToConfig(&cfg, tc)
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || cfg.Provider != config.ProviderOpenAI || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !runtimeProbeLoopback(u.Hostname()) {
		t.Fatal("diagnostic requires the actual default OpenAI-compatible loopback HTTP profile")
	}
	previousThinking, previousEffort := llm.ThinkingEnabled(), llm.ReasoningEffort()
	previousSampling, previousCache := llm.SamplingDefault(), llm.CachePromptDefault()
	previousDiscard := llm.DiscardPreviousReasoning()
	t.Cleanup(func() {
		llm.SetThinkingEnabled(previousThinking)
		_ = llm.SetReasoningEffort(previousEffort)
		llm.SetSamplingDefault(previousSampling)
		llm.SetCachePromptDefault(previousCache)
		llm.SetDiscardPreviousReasoning(previousDiscard)
	})
	config.ApplyLLMGlobals(tc, cfg.Temperature)
	expectedEffort := os.Getenv("SUPERCLI_COMPACTION_RUNTIME_EXPECTED_EFFORT")
	if err := runtimeCompactionCheckEffort(llm.ReasoningEffort(), expectedEffort); err != nil {
		t.Fatalf("%v (global=%q project=%q resolved=%q; sources=%s, %s)", err, global.ReasoningEffort, project.ReasoningEffort, tc.ReasoningEffort, globalPath, projectPath)
	}
	data, err := os.ReadFile(os.Getenv("SUPERCLI_COMPACTION_RUNTIME_RECEIPT"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Summary         string           `json:"summary"`
		Passed          bool             `json:"passed"`
		History         []llm.Message    `json:"history"`
		CompactEvent    AutoCompactEvent `json:"compact_event"`
		RequestMessages [][]llm.Message  `json:"request_messages"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil || !receipt.Passed {
		t.Fatalf("valid completed summary receipt required: %v", err)
	}
	inputMode := os.Getenv("SUPERCLI_COMPACTION_RUNTIME_INPUT")
	history := []llm.Message{{Role: llm.RoleUser, Content: receipt.Summary}}
	if inputMode == "prefix" {
		removed := receipt.CompactEvent.Removed
		if removed <= 0 || removed > len(receipt.History) || len(receipt.RequestMessages) == 0 || len(receipt.RequestMessages[0]) != 2 {
			t.Fatal("prefix diagnostic requires actual completed compaction history and original helper messages")
		}
		// Constructor/resume normalize assistant history before compaction.
		// Apply that production projection (including visible text parts and
		// stripped inline thinking), without modifying archived input bytes.
		projected := (&Loop{}).cleanModelHistory(receipt.History)
		if len(projected) != len(receipt.History) {
			t.Fatal("history projection changed message indices; original prefix cannot be reconstructed by count")
		}
		history = projected[:removed]
		if receipt.RequestMessages[0][0].Content != compactionPrompt || receipt.RequestMessages[0][1].Content != RenderCompactTranscript(history) {
			transcript := RenderCompactTranscript(history)
			t.Fatalf("prefix or compaction instruction differs from completed receipt: system bytes=%d/%d SHA=%s/%s transcript bytes=%d/%d SHA=%s/%s; do not present as identical input",
				len(compactionPrompt), len(receipt.RequestMessages[0][0].Content), compactionQualitySHA([]byte(compactionPrompt)), compactionQualitySHA([]byte(receipt.RequestMessages[0][0].Content)),
				len(transcript), len(receipt.RequestMessages[0][1].Content), compactionQualitySHA([]byte(transcript)), compactionQualitySHA([]byte(receipt.RequestMessages[0][1].Content)))
		}
	} else if inputMode == "" || inputMode == "summary" {
		inputMode = "summary"
		if !IsLegacyCompactionSummary(history[0]) {
			t.Fatal("completed receipt must contain a full prior-summary envelope")
		}
	} else {
		t.Fatal("INPUT must be prefix or summary")
	}
	if os.Getenv("SUPERCLI_COMPACTION_RUNTIME_VALIDATE_ONLY") == "1" {
		t.Logf("offline input and effort guards passed: input=%s messages=%d transcript=%d bytes effort=%s", inputMode, len(history), len(RenderCompactTranscript(history)), expectedEffort)
		return
	}
	parent, err := compactionQualityOutputParent(os.Getenv("SUPERCLI_COMPACTION_RUNTIME_OUT"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "runtime-")
	if err != nil {
		t.Fatal(err)
	}
	report := runtimeCompactionReport{
		InputMode: inputMode,
		Profile:   tc.DefaultProvider, Provider: cfg.Provider, Model: cfg.Model,
		GlobalConfig: globalPath, ProjectConfig: projectPath, GlobalEffort: global.ReasoningEffort, ProjectEffort: project.ReasoningEffort, ExpectedResolvedEffort: expectedEffort,
		ConfiguredThinking: llm.ThinkingEnabled(), ConfiguredEffort: llm.ReasoningEffort(), ConfiguredMaxTokens: cfg.MaxTokens,
		History: runtimeCompactionReportHistory(history), SourceReceiptSHA256: compactionQualitySHA(data),
		Transcript: RenderCompactTranscript(history), Instruction: compactionPrompt,
		Limitations: []string{
			"One runtime compatibility diagnostic; not a paired inference speed benchmark or backend reasoning guarantee.",
			"Actual config hierarchy includes the workspace override; this cannot prove behavior at a different global-only effort.",
			"Historical summary comes from a completed receipt; historical tools are not executed again.",
			"The only constructor difference is observational HTTP transport injection; retries are blocked before sending.",
			"No cache inference can be made when backend cache usage is absent.",
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	defer transport.CloseIdleConnections()
	wire := &runtimeCompactionTransport{inner: transport, host: u.Host}
	observer := &runtimeCompactionObserver{}
	var statsMu sync.Mutex
	defer func() {
		observer.mu.Lock()
		report.CompleteCalls, report.RawAnswer, report.FinishReason = observer.calls, strings.TrimSpace(stripThinking(observer.content.String())), observer.finish
		report.ToolFragments, report.UsageReported = observer.toolFragments, observer.usageReported
		report.StreamTiming = observer.timing
		observer.mu.Unlock()
		wire.mu.Lock()
		report.Wire = append([]runtimeCompactionWire(nil), wire.requests...)
		wire.mu.Unlock()
		statsMu.Lock()
		out, saveErr := json.MarshalIndent(report, "", "  ")
		statsMu.Unlock()
		path := filepath.Join(run, "receipt.json")
		if saveErr == nil {
			saveErr = os.WriteFile(path, out, 0600)
		}
		if saveErr != nil {
			t.Errorf("save runtime diagnostic: %v", saveErr)
		}
		t.Logf("actual runtime-profile diagnostic: %s", path)
	}()
	caps, err := llm.NewCapabilityRegistryFromSources(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	metadataCtx, cancelMetadata := context.WithTimeout(context.Background(), 2*time.Second)
	startedMetadata := time.Now()
	nativeModels := llm.ListLocalNativeModelInfos(metadataCtx, cfg.BaseURL, cfg.APIKey)
	report.MetadataDurationMS = time.Since(startedMetadata).Milliseconds()
	cancelMetadata()
	for _, native := range nativeModels {
		if native.ID != cfg.Model || !native.ReasoningKnown {
			continue
		}
		// Same merge as ensureLocalReasoningMetadata: unrelated catalog facts
		// remain intact; the native source owns only the reasoning controls.
		info, ok := caps.Get(native.ID)
		if !ok {
			info = llm.HeuristicCapabilities(native.ID)
		}
		info.Reasoning, info.ReasoningKnown, info.ReasoningToggleOnly = native.Reasoning, true, native.ReasoningToggleOnly
		caps.Register(info)
		report.Native = native
	}
	if !report.Native.ReasoningKnown {
		report.Error = "fresh native metadata did not identify selected model reasoning controls"
		t.Fatal(report.Error)
	}
	build := func(actual config.Config, _ string, registry *llm.CapabilityRegistry) (llm.Provider, error) {
		// Exact local constructor used by factory.Default, with HTTPClient
		// injected only to record/block traffic. No control fields are changed.
		provider, err := llm.NewOpenAI(llm.OpenAIConfig{
			BaseURL: actual.BaseURL, APIKey: actual.APIKey, Model: actual.Model,
			MaxTokens: actual.MaxTokens, Timeout: actual.Timeout, Capabilities: registry,
			HTTPClient: &http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		})
		if err != nil {
			return nil, err
		}
		observer.inner = provider
		return observer, nil
	}
	f := factory.New(build, dataDir, caps, func(stat llm.CallStat) {
		statsMu.Lock()
		report.Calls = append(report.Calls, stat)
		statsMu.Unlock()
	})
	provider, err := f.Build(cfg, llm.PurposeCompact)
	if err != nil {
		report.Error = err.Error()
		t.Fatal(err)
	}
	report.ReasoningState = llm.ProviderReasoningState(observer.inner)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	started := time.Now()
	report.Summary, err = SummarizeForCompaction(ctx, provider, history)
	report.DurationMS = time.Since(started).Milliseconds()
	observer.mu.Lock()
	if !observer.started.IsZero() {
		report.LocalBeforeCompleteNS = observer.started.Sub(started).Nanoseconds()
		if observer.timing.EOFNS != nil {
			report.LocalAfterEOFNS = time.Since(started).Nanoseconds() - report.LocalBeforeCompleteNS - *observer.timing.EOFNS
		}
	}
	observer.mu.Unlock()
	if err != nil {
		report.Error = err.Error()
		t.Fatal(err)
	}
}

func runtimeProbeLoopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func runtimeCompactionCheckEffort(actual, expected string) error {
	if expected == "" || actual != expected {
		return fmt.Errorf("explicit expected resolved effort required: actual=%q expected=%q; no model request sent", actual, expected)
	}
	return nil
}

// Test receipts retain visible task evidence only. Original native reasoning
// remains in its existing source receipt; never duplicate it into timing data.
func runtimeCompactionReportHistory(messages []llm.Message) []llm.Message {
	copyMessages := append([]llm.Message(nil), messages...)
	for i, message := range messages {
		if message.Role != llm.RoleAssistant {
			continue
		}
		copyMessages[i].Parts = nil
		for _, part := range message.Parts {
			if part.Type != llm.PartTypeReasoning {
				copyMessages[i].Parts = append(copyMessages[i].Parts, part)
			}
		}
	}
	return copyMessages
}

func TestCompactionRuntimeExpectedEffortGuard(t *testing.T) {
	for _, tc := range []struct {
		actual, expected string
		wantError        bool
	}{
		{"xhigh", "xhigh", false},
		{"max", "max", false},
		{"xhigh", "max", true},
		{"max", "xhigh", true},
		{"xhigh", "", true},
		{"", "", true},
	} {
		if err := runtimeCompactionCheckEffort(tc.actual, tc.expected); (err != nil) != tc.wantError {
			t.Fatalf("effort guard actual=%q expected=%q error=%v", tc.actual, tc.expected, err)
		}
	}
}

type runtimeCompactionReport struct {
	InputMode                                    string
	LocalBeforeCompleteNS, LocalAfterEOFNS       int64
	StreamTiming                                 runtimeCompactionStreamTiming
	GlobalConfig, ProjectConfig                  string
	GlobalEffort, ProjectEffort                  string
	ExpectedResolvedEffort                       string
	Profile, Provider, Model                     string
	ConfiguredThinking                           bool
	ConfiguredEffort                             string
	ConfiguredMaxTokens                          int
	ReasoningState                               llm.ReasoningState
	Native                                       llm.ModelInfo
	History                                      []llm.Message
	SourceReceiptSHA256, Transcript, Instruction string
	Summary, RawAnswer, Error, FinishReason      string
	MetadataDurationMS, DurationMS               int64
	CompleteCalls, ToolFragments                 int
	UsageReported                                bool
	Calls                                        []llm.CallStat
	Wire                                         []runtimeCompactionWire
	Limitations                                  []string
}

type runtimeCompactionWire struct {
	Body                                                                        json.RawMessage
	SHA256                                                                      string
	HTTPStatus                                                                  int
	HTTPError                                                                   string
	Blocked                                                                     bool
	StartedAt                                                                   time.Time
	ConnectionReadyNS, RequestWrittenNS, FirstResponseByteNS, ResponseHeadersNS *int64
}

// These are client-observed boundaries, never inferred backend compute times.
// NativeReasoning may arrive only as a complete replay block at EOF; that alone
// cannot establish when reasoning started. Only exposed reasoning text or an
// explicit ReasoningStarted marker supplies that timestamp. No thoughts saved.
type runtimeCompactionStreamTiming struct {
	FirstModelOutputNS *int64
	FirstReasoningNS   *int64
	FirstContentNS     *int64
	FinishNS           *int64
	EOFNS              *int64
	ReasoningBytes     int
	ContentBytes       int
}

func (s *runtimeCompactionStreamTiming) observe(d llm.Delta, elapsed time.Duration) {
	stamp := func(dst **int64) {
		if *dst == nil {
			ns := elapsed.Nanoseconds()
			*dst = &ns
		}
	}
	if d.HasModelOutput() {
		stamp(&s.FirstModelOutputNS)
	}
	if d.Err == nil && d.Notice == "" {
		if d.ReasoningStarted || d.Reasoning != "" {
			stamp(&s.FirstReasoningNS)
		}
		if d.Content != "" {
			stamp(&s.FirstContentNS)
		}
		s.ReasoningBytes += len(d.Reasoning)
		s.ContentBytes += len(d.Content)
	}
	if d.FinishReason != "" {
		stamp(&s.FinishNS)
	}
}

type runtimeCompactionObserver struct {
	inner         llm.Provider
	mu            sync.Mutex
	started       time.Time
	calls         int
	content       strings.Builder
	finish        string
	toolFragments int
	usageReported bool
	timing        runtimeCompactionStreamTiming
}

func (p *runtimeCompactionObserver) Name() string { return p.inner.Name() }

func (p *runtimeCompactionObserver) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.calls++
	p.started = time.Now()
	p.mu.Unlock()
	if len(tools) != 0 {
		return nil, fmt.Errorf("runtime compactor must not offer tools")
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
			case d, ok := <-in:
				p.mu.Lock()
				if !ok {
					ns := time.Since(p.started).Nanoseconds()
					p.timing.EOFNS = &ns
					p.mu.Unlock()
					return
				}
				p.timing.observe(d, time.Since(p.started))
				p.content.WriteString(d.Content)
				if d.FinishReason != "" {
					p.finish = d.FinishReason
				}
				if d.ToolCall != nil {
					p.toolFragments++
				}
				p.usageReported = p.usageReported || d.Usage != nil
				p.mu.Unlock()
				select {
				case out <- d:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func TestCompactionRuntimeStreamTimingEvidence(t *testing.T) {
	var timing runtimeCompactionStreamTiming
	// Synthetic streaming boundaries test instrumentation only, not model speed.
	timing.observe(llm.Delta{Role: llm.RoleAssistant}, time.Millisecond)
	timing.observe(llm.Delta{Notice: "retry metadata"}, 2*time.Millisecond)
	timing.observe(llm.Delta{Usage: &llm.Usage{Input: 1}}, 3*time.Millisecond)
	if timing.FirstModelOutputNS != nil || timing.FirstReasoningNS != nil || timing.FirstContentNS != nil {
		t.Fatal("metadata was counted as generated output")
	}
	timing.observe(llm.Delta{ReasoningStarted: true}, 4*time.Millisecond)
	timing.observe(llm.Delta{Reasoning: "synthetic reasoning"}, 5*time.Millisecond)
	timing.observe(llm.Delta{Content: "synthetic answer"}, 9*time.Millisecond)
	timing.observe(llm.Delta{FinishReason: "stop"}, 10*time.Millisecond)
	if *timing.FirstModelOutputNS != int64(4*time.Millisecond) || *timing.FirstReasoningNS != int64(4*time.Millisecond) || *timing.FirstContentNS != int64(9*time.Millisecond) || *timing.FinishNS != int64(10*time.Millisecond) {
		t.Fatalf("stream boundaries changed: %+v", timing)
	}
	var hidden runtimeCompactionStreamTiming
	hidden.observe(llm.Delta{NativeReasoning: &llm.ReasoningBlock{}}, time.Second)
	if hidden.FirstReasoningNS != nil {
		t.Fatal("completed native reasoning block was presented as a reasoning-start timestamp")
	}
}

type runtimeCompactionTransport struct {
	inner    http.RoundTripper
	host     string
	mu       sync.Mutex
	requests []runtimeCompactionWire
}

func (p *runtimeCompactionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != p.host || req.URL.Scheme != "http" {
		return nil, fmt.Errorf("runtime diagnostic may contact only selected loopback endpoint")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	p.mu.Lock()
	index := len(p.requests)
	started := time.Now()
	p.requests = append(p.requests, runtimeCompactionWire{Body: body, SHA256: compactionQualitySHA(body), Blocked: index != 0, StartedAt: started})
	p.mu.Unlock()
	if index != 0 {
		return nil, fmt.Errorf("runtime diagnostic blocked compatibility retry before HTTP: selected control must remain unchanged")
	}
	// These are HTTP boundaries, not inferred backend prefill/queue timings.
	stamp := func(field func(*runtimeCompactionWire) **int64) {
		elapsed := time.Since(started).Nanoseconds()
		p.mu.Lock()
		destination := field(&p.requests[index])
		if *destination == nil {
			*destination = &elapsed
		}
		p.mu.Unlock()
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			stamp(func(w *runtimeCompactionWire) **int64 { return &w.ConnectionReadyNS })
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			stamp(func(w *runtimeCompactionWire) **int64 { return &w.RequestWrittenNS })
		},
		GotFirstResponseByte: func() { stamp(func(w *runtimeCompactionWire) **int64 { return &w.FirstResponseByteNS }) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	response, err := p.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	stamp(func(w *runtimeCompactionWire) **int64 { return &w.ResponseHeadersNS })
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
	p.requests[index].HTTPStatus, p.requests[index].HTTPError = response.StatusCode, string(errorBody)
	p.mu.Unlock()
	return response, nil
}
