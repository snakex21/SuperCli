package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

const downloadFixtureArgs = "{\"tool\":\"web_download\",\"args\":{\"url\":\"https://example.com/a.png\",\"path\":\"assets/a.png\"}}"

func TestDownloadReferenceRequiresExplicitAction(t *testing.T) {
	for _, prompt := range []string{"Pobierz obrazek z https://example.com/image.png do assets/a.png", "Can you download https://example.com/font.woff2 into assets/font.woff2?", "Ściągnij https://example.com/archive.zip", "web_download https://example.com/direct?id=1", "cześć pobierz mi do pobranych jakiś fajny gif z tenora", "download game assets", "Please download a GIF from Tenor", "Znajdź gif w internecie i pobierz go", "Find a GIF online and download it", "Nie pobierz A; pobierz B"} {
		if !containsFileDownloadReference(prompt) {
			t.Errorf("explicit download missed: %q", prompt)
		}
	}
	for _, prompt := range []string{"cześć", "read https://example.com/about-download", "https://example.com/download.png", "Jak pobierzemy https://example.com/a.zip?", "Jak pobierz https://example.com/a.zip", "How to download https://example.com/a.zip", "Do not download https://example.com/a.png", "Don't download https://example.com/a.png", "Nie pobierz https://example.com/a.png", "read instructions from https://example.com/download", "Znajdź mi coś", "Znajdź gif z tenora", "Nie pobierz GIF ani download pliku", "Do not find or download a GIF online", "The model tried to download a GIF to Downloads", "Explain the command: pobierz gif z Tenora", "Przykład: pobierz gif z tenora", "Przykład:\n pobierz gif z tenora", "Example:\n download a GIF from Tenor", "W logu: pobierz do Pobranych", "The prompt says `download a GIF from Tenor`", "\"pobierz gif z tenora\"", "The word download occurs in this message"} {
		if containsFileDownloadReference(prompt) {
			t.Errorf("unrelated turn exposed download: %q", prompt)
		}
	}
}

func TestSelfContainedWebRequestKeepsLocalAndMixedWork(t *testing.T) {
	for _, prompt := range []string{
		"cześć pobierz mi do pobranych jakiś fajny gif z tenora",
		"Download https://example.com/file.gif to assets/file.gif",
		"Znajdź mi ciekawy gif w internecie", "Can you find a funny GIF on Tenor?",
		"Search the web for current weather", "Find GIFs at https://tenor.com/",
	} {
		if !isSelfContainedWebRequest(prompt) {
			t.Errorf("web-only request missed: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"cześć", "znajdź mi coś", "Znajdź plik", "Find download references in this repository",
		"download game assets", "How to download a GIF from Tenor?", "Nie pobierz gif z tenora",
		"Pobierz gif z tenora i uruchom testy", "Pobierz https://example.com/code.gif do projektu",
		"Find documentation online and implement this function", "Znajdź GIF w internecie i napraw kod",
		"The model tried to download a GIF from Tenor", "Example: download a GIF from Tenor",
		"Wyjaśnij polecenie \"pobierz gif z tenora\"",
		"Find web assets in my local folder", "Search web references in files", "Znajdź plik z internetu na dysku",
		"Find web assets in a folder and download them",
		"Pobierz gif z Tenora i usuń stare logi", "Download a GIF from Tenor and rename old files",
		"Download https://example.com/setup.exe and install it",
	} {
		if isSelfContainedWebRequest(prompt) {
			t.Errorf("project/ambiguous request skipped normal routing: %q", prompt)
		}
	}
}

func TestDownloadPageContractDistinguishesPageAndMediaHost(t *testing.T) {
	for _, prompt := range []string{"pobierz gif z tenora", "download https://tenor.com/pl/view/funny", "download https://www.giphy.com/gifs/funny", "download https://animations.example.test/view/funny", "download https://assets.example.test/download?id=2", "download https://example.test/about.html", "download https://example.test/asset.unknown", "download https://assets.example.test/a.gif and https://animations.example.test/view/other"} {
		if !needsDownloadPageContract(prompt) {
			t.Errorf("page contract missing: %q", prompt)
		}
	}
	for _, prompt := range []string{"download https://example.com/direct.gif", "download https://media.tenor.com/a.gif", "download https://media1.tenor.com/m/a.gif", "download https://media.giphy.com/media/a/giphy.gif", "download https://animations.example.test/a.GIF?size=large", "download [font](https://assets.example.test/font.woff2)", "download https://tenor.com/direct.gif", "download https://giphy.com/direct.png"} {
		if needsDownloadPageContract(prompt) {
			t.Errorf("direct media URL paid for page contract: %q", prompt)
		}
	}
}

func TestWebDownloadSearchContractsExpireAndKeepCachePrefix(t *testing.T) {
	l := downloadRequestedFixture(t, true)
	l.registry.MustRegister(tools.NewWebSearch("", "").LookupSpec())
	l.registry.MarkAlwaysOn("web_lookup")
	l.registry.MustRegister(tools.NewWebFetch().Spec())
	l.prepareRunRoute(context.Background(), "napraw plik")
	wireBefore, _ := json.Marshal(l.buildToolDefs())
	ordinary, ordinaryTokens := l.prepareProviderMessages(true)
	l.prepareRunRoute(context.Background(), "cześć pobierz mi do pobranych jakiś fajny gif z tenora")
	wireAfter, _ := json.Marshal(l.buildToolDefs())
	requested, tokens := l.prepareProviderMessages(true)
	if !bytes.Equal(wireBefore, wireAfter) || ordinary[0].Content != requested[0].Content {
		t.Fatal("search-download changed cacheable prefix")
	}
	contracts := requestToolContracts(requested)
	if len(contracts) != 1 || !requestContainsToolContract(requested, "web_download", l.buildToolDefs()) || !requestContainsToolContract(requested, "web_fetch", l.buildToolDefs()) {
		t.Fatalf("contracts=%v", contracts)
	}
	if tokens+estimateRequestTokens(nil, l.buildToolDefs()) != estimateRequestTokens(requested, l.buildToolDefs()) {
		t.Fatal("request contract estimate wrong")
	}
	t.Logf("search-download contracts add %d estimated message tokens", tokens-ordinaryTokens)
	l.prepareRunRoute(context.Background(), "Pobierz https://example.com/direct.gif")
	direct, directTokens := l.prepareProviderMessages(true)
	if len(requestToolContracts(direct)) != 1 || l.downloadPageForRun || directTokens != tokens || !containsNativeContract(l.buildToolDefs(), "web_fetch") {
		t.Fatal("direct asset duplicated the already native page-reader contract")
	}
	l.prepareRunRoute(context.Background(), "cześć")
	if l.downloadForRun || l.downloadPageForRun || l.requestedToolContext() != "" || len(l.registry.DiscoveredNames()) != 0 {
		t.Fatal("search-download context leaked into a greeting or discovery")
	}
}

type searchDownloadProvider struct {
	t         *testing.T
	calls     int
	defs      []byte
	prefix    string
	firstCost int
	lastCost  int
}

func (p *searchDownloadProvider) Name() string { return "search-download-fixture" }
func (p *searchDownloadProvider) Complete(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	raw, _ := json.Marshal(defs)
	if p.calls == 1 {
		p.defs, p.prefix = raw, messages[0].Content
		p.firstCost = estimateRequestTokens(messages, defs)
	} else if !bytes.Equal(p.defs, raw) || p.prefix != messages[0].Content {
		p.t.Fatal("search/download workflow changed stable prefix")
	}
	p.lastCost = estimateRequestTokens(messages, defs)
	if !requestContainsToolContract(messages, "web_download", defs) || !requestContainsToolContract(messages, "web_fetch", defs) {
		p.t.Fatal("workflow lacks scoped download/page-reader contracts")
	}
	ch := make(chan llm.Delta, 1)
	var call llm.ToolCall
	switch p.calls {
	case 1:
		call = llm.ToolCall{ID: "search", Name: "web_lookup", Arguments: "{\"query\":\"funny gif site:tenor.com\"}"}
	case 2:
		call = llm.ToolCall{ID: "page", Name: invokeToolName, Arguments: "{\"tool\":\"web_fetch\",\"args\":{\"url\":\"https://tenor.com/view/fixture\",\"max_chars\":2000}}"}
	case 3:
		call = llm.ToolCall{ID: "download", Name: invokeToolName, Arguments: "{\"tool\":\"web_download\",\"args\":{\"url\":\"https://media.tenor.com/fixture.gif\",\"path\":\"assets/funny.gif\"}}"}
	case 4:
		ch <- llm.Delta{Content: "saved assets/funny.gif", FinishReason: "stop"}
	default:
		p.t.Fatal("unexpected extra model continuation")
	}
	if call.Name != "" {
		ch <- llm.Delta{ToolCall: &call, FinishReason: "tool_calls"}
	}
	close(ch)
	return ch, nil
}

func TestPublicSearchDownloadRunSkipsBothDiscoveryCalls(t *testing.T) {
	l := downloadRequestedFixture(t, true)
	reg := tools.NewRegistry()
	executed := map[string]int{}
	lookup := tools.NewWebSearch("", "").LookupSpec()
	lookup.Fn = func(context.Context, json.RawMessage) (tools.Result, error) {
		executed["web_lookup"]++
		return tools.Result{Text: "1. Funny GIF — https://tenor.com/view/fixture"}, nil
	}
	page := tools.NewWebFetch().Spec()
	page.Fn = func(_ context.Context, args json.RawMessage) (tools.Result, error) {
		executed["web_fetch"]++
		return tools.Result{Text: "Media URL: https://media.tenor.com/fixture.gif"}, nil
	}
	download := tools.NewWebDownload(t.TempDir()).Spec()
	download.Fn = func(_ context.Context, args json.RawMessage) (tools.Result, error) {
		executed["web_download"]++
		var a struct{ URL, Path string }
		if json.Unmarshal(args, &a) != nil || a.URL != "https://media.tenor.com/fixture.gif" || a.Path != "assets/funny.gif" {
			t.Fatalf("download arguments changed: %s", args)
		}
		return tools.Result{Text: "Downloaded assets/funny.gif (fixture)"}, nil
	}
	for _, spec := range []tools.Tool{lookup, page, download} {
		reg.MustRegister(spec)
	}
	reg.MarkAlwaysOn("web_lookup")
	reg.MustRegister(NewInvokeTool(reg).Spec())
	reg.MarkAlwaysOn(invokeToolName)
	reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
	reg.MarkAlwaysOn("tool_search")
	l.SetRegistry(reg)
	p := &searchDownloadProvider{t: t}
	l.provider = p
	collections := 0
	l.SetNextCoordinatorAddonSource(func(context.Context) string { collections++; return testRepoBlock })
	drainEvents(t, mustRun(t, l, "cześć pobierz mi do pobranych jakiś fajny gif z tenora"))
	if p.calls != 4 || executed["web_lookup"] != 1 || executed["web_fetch"] != 1 || executed["web_download"] != 1 || collections != 0 || len(reg.DiscoveredNames()) != 0 {
		t.Fatalf("provider=%d tools=%v preflight=%d discovery=%v", p.calls, executed, collections, reg.DiscoveredNames())
	}
	for _, message := range l.Messages {
		if strings.Contains(message.Content, requestedToolContextPreamble) {
			t.Fatal("scoped contracts persisted in conversation")
		}
		for _, call := range message.ToolCalls {
			if call.Name == "tool_search" || call.Name == invokeToolName {
				t.Fatal("workflow retained discovery or dispatcher attribution")
			}
		}
	}
	t.Logf("fixture: %d provider requests, 3 tool calls, zero discovery/navigator/preflight; request estimates=%d first, %d final", p.calls, p.firstCost, p.lastCost)
}

func BenchmarkSelfContainedWebDownloadIntent(b *testing.B) {
	prompt := "cześć pobierz mi do pobranych jakiś fajny gif z tenora"
	b.ReportAllocs()
	for b.Loop() {
		_ = isSelfContainedWebRequest(prompt)
	}
}

func TestWebOnlyCaptionWithAttachmentRetainsRepositoryContext(t *testing.T) {
	l := downloadRequestedFixture(t, true)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: "download a GIF from Tenor"},
		{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "attached-project-evidence", Active: true}},
	}})
	collections := 0
	l.SetNextCoordinatorAddonSource(func(context.Context) string { collections++; return testRepoBlock })
	l.prepareRunRoute(context.Background(), "download a GIF from Tenor")
	if collections != 1 || l.route != RouteCoordinator || !strings.Contains(l.Messages[len(l.Messages)-1].TextOnly().Content, testRepoBlock) {
		t.Fatal("attachment caption discarded project evidence")
	}
}

func TestWebDownloadSearchNativeFallbackSnapshotExpires(t *testing.T) {
	l := downloadRequestedFixture(t, false)
	l.registry.MustRegister(tools.NewWebFetch().Spec())
	l.prepareRunRoute(context.Background(), "download https://example.com/a.gif")
	before, _ := json.Marshal(l.buildToolDefs())
	l.prepareRunRoute(context.Background(), "download a funny GIF from Tenor")
	found := false
	for _, def := range l.buildToolDefs() {
		found = found || def.Name == "web_fetch"
	}
	if !found || l.requestedToolContext() != "" {
		t.Fatal("native fallback lost the scoped page-reader contract")
	}
	l.prepareRunRoute(context.Background(), "download https://example.com/a.gif")
	after, _ := json.Marshal(l.buildToolDefs())
	if !bytes.Equal(before, after) {
		t.Fatal("page-reader contract was cached after scope expired")
	}
}

func TestWebOnlyRouteSkipsNavigatorAndPreservesDeferredPreflight(t *testing.T) {
	for _, addon := range []string{"eager", "lazy"} {
		t.Run(addon, func(t *testing.T) {
			provider := &capturingProvider{reply: "done"}
			navigator := &stubProvider{name: "navigator-must-stay-unused"}
			loop, err := NewLoop(LoopConfig{Provider: provider, NavigatorProvider: navigator, EnableNavigator: true, Registry: tools.NewRegistry(), System: "stable-prefix"})
			if err != nil {
				t.Fatal(err)
			}
			collections := 0
			if addon == "lazy" {
				loop.SetNextCoordinatorAddonSource(func(context.Context) string { collections++; return testRepoBlock })
			} else {
				loop.SetNextCoordinatorAddon(testRepoBlock)
			}
			for _, prompt := range []string{"cześć pobierz mi do pobranych gif z tenora", "Znajdź mi GIF w internecie"} {
				drainEvents(t, mustRun(t, loop, prompt))
			}
			if collections != 0 || navigator.calls != 0 || len(provider.requests()) != 2 {
				t.Fatalf("web-only added work: collection=%d navigator=%d requests=%d", collections, navigator.calls, len(provider.requests()))
			}
			for _, req := range provider.requests() {
				for _, msg := range req {
					if strings.Contains(msg.Content, testRepoBlock) {
						t.Fatal("web-only turn received repo context")
					}
				}
			}
			// Keep the next coordinator turn deterministic; the earlier web
			// turns must have preserved its eagerly queued or lazy repo addon.
			loop.navigate = false
			drainEvents(t, mustRun(t, loop, "Pobierz gif z tenora i uruchom testy projektu"))
			if addon == "lazy" && collections != 1 {
				t.Fatalf("project collection=%d", collections)
			}
			seen := false
			for _, msg := range provider.requests()[2] {
				seen = seen || strings.Contains(msg.Content, testRepoBlock)
			}
			if !seen {
				t.Fatal("mixed project turn lost queued repo context")
			}
		})
	}
}

func downloadRequestedFixture(t *testing.T, dispatcher bool) *Loop {
	t.Helper()
	l := requestedContextFixture(t, dispatcher)
	l.registry.MustRegister(tools.NewWebDownload(t.TempDir()).Spec())
	return l
}

func TestDownloadRequestKeepsStablePrefixAndExpires(t *testing.T) {
	l := downloadRequestedFixture(t, true)
	l.prepareRunRoute(context.Background(), "napraw plik")
	defsBefore, _ := json.Marshal(l.buildToolDefs())
	ordinary, _ := l.prepareProviderMessages(true)
	l.prepareRunRoute(context.Background(), "Pobierz https://example.com/a.png do assets/a.png")
	if l.route != RouteCoordinator {
		t.Fatal("download lost project tools")
	}
	defsAfter, _ := json.Marshal(l.buildToolDefs())
	if !bytes.Equal(defsBefore, defsAfter) {
		t.Fatal("stable schemas changed")
	}
	messages, tokens := l.prepareProviderMessages(true)
	if ordinary[0].Content != messages[0].Content {
		t.Fatal("cacheable prefix changed")
	}
	contracts := requestToolContracts(messages)
	if len(contracts) != 1 || contracts[0].Name != "web_download" {
		t.Fatalf("contracts=%v", contracts)
	}
	spec, _ := l.registry.Get("web_download")
	var wanted, actual any
	_ = json.Unmarshal([]byte(spec.Schema), &wanted)
	_ = json.Unmarshal([]byte(contracts[0].Schema), &actual)
	wantedJSON, _ := json.Marshal(wanted)
	actualJSON, _ := json.Marshal(actual)
	if contracts[0].Description != spec.Description || !bytes.Equal(wantedJSON, actualJSON) {
		t.Fatal("contract altered")
	}
	if l.registry.IsActive("web_download") || len(l.registry.DiscoveredNames()) != 0 {
		t.Fatal("request became persistent activation")
	}
	if tokens+estimateRequestTokens(nil, l.buildToolDefs()) != estimateRequestTokens(messages, l.buildToolDefs()) {
		t.Fatal("contract estimate wrong")
	}
	for _, msg := range l.Messages {
		if strings.Contains(msg.Content, requestedToolContextPreamble) {
			t.Fatal("contract persisted in history")
		}
	}
	l.prepareRunRoute(context.Background(), "cześć")
	if l.downloadForRun || l.requestedToolContext() != "" {
		t.Fatal("contract leaked into next run")
	}
	finalDefs, _ := json.Marshal(l.buildToolDefs())
	if !bytes.Equal(defsBefore, finalDefs) {
		t.Fatal("core changed after allowance removal")
	}
}

func TestDownloadFallbackSnapshotAndRestrictions(t *testing.T) {
	l := downloadRequestedFixture(t, false)
	l.prepareRunRoute(context.Background(), "napraw plik")
	before, _ := json.Marshal(l.buildToolDefs())
	l.prepareRunRoute(context.Background(), "download https://example.com/a.png")
	has := false
	for _, def := range l.buildToolDefs() {
		has = has || def.Name == "web_download"
	}
	if !has || l.requestedToolContext() != "" {
		t.Fatal("native fallback lost contract")
	}
	l.prepareRunRoute(context.Background(), "napraw plik")
	after, _ := json.Marshal(l.buildToolDefs())
	if !bytes.Equal(before, after) {
		t.Fatal("stale snapshot")
	}
	l = downloadRequestedFixture(t, true)
	l.prepareRunRoute(context.Background(), "download https://example.com/a.png")
	bad := "{\"tool\":\"web_download\",\"args\":{\"url\":\"https://example.com/a.png\",\"path\":\"a.png\",\"max_bytes\":268435457}}"
	calls := l.resolveInvokeToolCalls([]llm.ToolCall{{Name: invokeToolName, Arguments: bad}})
	if calls[0].Name != "web_download" {
		t.Fatal("requested dispatch unavailable")
	}
	result, err := l.registry.Execute(context.Background(), calls[0].Name, json.RawMessage(calls[0].Arguments))
	if err == nil && result.Err == nil {
		t.Fatal("target maximum validation bypassed")
	}
	l.prepareRunRoute(context.Background(), "napraw plik")
	dormant := l.resolveInvokeToolCalls([]llm.ToolCall{{Name: invokeToolName, Arguments: downloadFixtureArgs}})
	if dormant[0].Name != invokeToolName {
		t.Fatal("dispatch leaked across runs")
	}
	l.prepareRunRoute(context.Background(), "download https://example.com/a.png")
	l.finalReplyOnly = true
	if l.buildToolDefs() != nil || l.requestedToolContext() != "" {
		t.Fatal("final-only reply gained tools")
	}
	l.finalReplyOnly = false
	l.SetRegistry(tools.NewRegistry())
	if l.requestedToolContext() != "" {
		t.Fatal("restricted registry gained download")
	}
}

type requestedDownloadProvider struct {
	t      *testing.T
	calls  int
	defs   []byte
	prefix string
}

func (p *requestedDownloadProvider) Name() string { return "download-fixture" }
func (p *requestedDownloadProvider) Complete(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	raw, _ := json.Marshal(defs)
	if p.calls == 1 {
		p.defs = raw
		p.prefix = messages[0].Content
	} else if !bytes.Equal(raw, p.defs) || p.prefix != messages[0].Content {
		p.t.Fatal("workflow changed prefix")
	}
	if !requestContainsToolContract(messages, "web_download", defs) {
		p.t.Fatal("workflow lacked exact contract")
	}
	ch := make(chan llm.Delta, 1)
	if p.calls == 1 {
		call := llm.ToolCall{ID: "asset", Name: invokeToolName, Arguments: downloadFixtureArgs}
		ch <- llm.Delta{ToolCall: &call, FinishReason: "tool_calls"}
	} else {
		ch <- llm.Delta{Content: "saved", FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}
func TestPublicDownloadRunSkipsDiscoveryAndPreservesValidation(t *testing.T) {
	l := downloadRequestedFixture(t, true)
	spec, _ := l.registry.Get("web_download")
	executed := 0
	spec.Fn = func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
		executed++
		var args struct{ URL, Path string }
		if json.Unmarshal(raw, &args) != nil || args.URL != "https://example.com/a.png" || args.Path != "assets/a.png" {
			t.Fatalf("arguments changed: %s", raw)
		}
		return tools.Result{Text: "Downloaded assets/a.png (fixture)"}, nil
	}
	reg := tools.NewRegistry()
	reg.MustRegister(spec)
	reg.MustRegister(NewInvokeTool(reg).Spec())
	reg.MarkAlwaysOn(invokeToolName)
	l.SetRegistry(reg)
	p := &requestedDownloadProvider{t: t}
	l.provider = p
	drainEvents(t, mustRun(t, l, "Pobierz https://example.com/a.png do assets/a.png"))
	if p.calls != 2 || executed != 1 || l.InvokeToolDispatches() != 1 {
		t.Fatalf("provider=%d executed=%d dispatch=%d", p.calls, executed, l.InvokeToolDispatches())
	}
	for _, msg := range l.Messages {
		if strings.Contains(msg.Content, requestedToolContextPreamble) {
			t.Fatal("contract persisted")
		}
		for _, call := range msg.ToolCalls {
			if call.Name == invokeToolName || call.Name == "tool_search" {
				t.Fatal("unnecessary discovery or lost target attribution")
			}
		}
	}
}
