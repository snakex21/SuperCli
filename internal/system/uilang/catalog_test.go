package uilang

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"
)

var formatPlaceholders = regexp.MustCompile(`%(?:\[\d+\])?[+\-# 0]*(?:\d+|\*)?(?:\.(?:\d+|\*))?(?:\[\d+\])?[vTtbcdoOxXUeEfFgGspqxw%]`)
var technicalTokens = regexp.MustCompile(`https?://[^\s]+|\.supercli/[^\s]*|(?:Ctrl|Shift|Alt|Meta)\+[A-Za-z0-9+]+|/(?:providers toggle|shuffle (?:interval|load|add)|update check\|download\|install)|/[a-z][a-z0-9_-]*|--[a-z][a-z0-9-]*|\b(?:SuperCli|ChatGPT|Codex|OpenAI|Anthropic|OpenCode|MCP|REVISE|TTFT|TOML|JSON|ZIP|Markdown|llama\.cpp|config\.toml|SUPERCLI_[A-Z_]+)\b|\b[a-z]+_[a-z_]+\b|<[^<>]+>`)
var providerIdentifiers = regexp.MustCompile(`Claude|Opus|Sonnet|Haiku|Gemini|Llama|Mixtral|Mistral Large|Mistral|Codestral|DeepSeek-V3|DeepSeek-R1|Grok-3|Grok-2|HF|Kilo AI|OpenCode Zen|OpenCode Go|Sonar|Qwen|Command R\+|Aya|NVIDIA NIM|Lepton AI|Zhipu|GLM-5\.2|Kimi|K3|K2\.6|Alibaba Qwen Standard|Alibaba Qwen Token Plan|ByteDance Volcengine Ark|StepFun|01\.AI|Yi-Lightning|Yi-Large|Xiaomi MiMo AI|Xiaomi MiMo Token Plan|V2\.5-Pro|tp-key|MiniMax M3|SiliconFlow|Baichuan AI|Tencent Hunyuan|TokenHub|Featherless|iFlytek Spark|Baidu ERNIE Qianfan|SenseTime SenseNova|LM Studio|Ollama|USD|RMB|[$¥]\d+(?:-\d+)?(?:/mo)?|\b\d+(?:\.\d+)?[+M]?\b`)

func providerValues(text string) []string {
	values := providerIdentifiers.FindAllString(text, -1)
	sort.Strings(values)
	return values
}

func technicalValues(text string) []string {
	var values []string
	for _, position := range technicalTokens.FindAllStringIndex(text, -1) {
		value := text[position[0]:position[1]]
		// A slash command starts at a word boundary. UI prose such as
		// load/pull or public/no key is translated as ordinary prose.
		if strings.HasPrefix(value, "/") && position[0] > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:position[0]])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) || previous == '_' {
				continue
			}
		}
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func placeholders(text string) []string {
	var result []string
	for _, position := range formatPlaceholders.FindAllStringIndex(text, -1) {
		// Static UI prose can contain a numerical percentage such as 60%.
		// It is not a printf directive even when the following word happens
		// to begin with a valid verb letter.
		prefix := strings.TrimRightFunc(text[:position[0]], unicode.IsSpace)
		if len(prefix) > 0 && prefix[len(prefix)-1] >= '0' && prefix[len(prefix)-1] <= '9' {
			continue
		}
		result = append(result, text[position[0]:position[1]])
	}
	return result
}

func TestTechnicalValuesDistinguishCommandsFromProse(t *testing.T) {
	if values := technicalValues("load/pull context/token next/save on/off quit/cancel public/no key ładowanie/zapis"); len(values) != 0 {
		t.Fatalf("ordinary prose classified as syntax: %v", values)
	}
	want := []string{"--update", ".supercli/cache", "/update", "https://example.test/file.zip"}
	if got := technicalValues("/update --update .supercli/cache https://example.test/file.zip"); !reflect.DeepEqual(got, want) {
		t.Fatalf("technical syntax changed: %v", got)
	}
	if got := placeholders("60% of context / 60 % og kontekst"); len(got) != 0 {
		t.Fatalf("prose percentage classified as printf: %v", got)
	}
	if reflect.DeepEqual(technicalValues("/providers toggle <provider>::<model_id>"), technicalValues("/providers przełącznik <provider>::<model_id>")) {
		t.Fatal("translated subcommand was accepted as executable syntax")
	}
	if reflect.DeepEqual(providerValues("Mistral Large, Codestral"), providerValues("Mistral Duży, Codestral")) {
		t.Fatal("translated model name was accepted as a provider identity")
	}
}

func TestCatalogCompletenessAndPlaceholderParity(t *testing.T) {
	english := defaultCatalogStore.catalog(English)
	if len(english) < 700 {
		t.Fatalf("UI catalog unexpectedly small: %d", len(english))
	}
	for _, language := range Languages() {
		t.Run(language.Code, func(t *testing.T) {
			data, err := catalogFiles.ReadFile("catalogs/" + language.Code + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.Valid(data) {
				t.Fatal("catalog is not UTF-8")
			}
			var catalog map[string]string
			if err := json.Unmarshal(data, &catalog); err != nil {
				t.Fatal(err)
			}
			if len(catalog) != len(english) {
				t.Errorf("catalog has %d keys, English has %d", len(catalog), len(english))
			}
			translated := 0
			for key, source := range english {
				value, ok := catalog[key]
				if !ok || strings.TrimSpace(value) == "" {
					t.Errorf("missing translation: %s", key)
					continue
				}
				if strings.Contains(value, "ZXQ") || strings.Contains(value, "QXZ") {
					t.Errorf("translation marker leaked: %s", key)
				}
				if !reflect.DeepEqual(placeholders(source), placeholders(value)) {
					t.Errorf("placeholder mismatch in %s: %q / %q", key, source, value)
				}
				if !reflect.DeepEqual(technicalValues(source), technicalValues(value)) {
					t.Errorf("technical value mismatch in %s: %q / %q", key, source, value)
				}
				if strings.HasPrefix(key, "provider.description.") && !reflect.DeepEqual(providerValues(source), providerValues(value)) {
					t.Errorf("provider identity or numeric value mismatch in %s: %q / %q", key, source, value)
				}
				if value != source {
					translated++
				}
			}
			// Product names and command syntax may be identical. An English clone
			// must never pass as a supported translation catalog.
			if language.Code != English && translated < len(english)*3/4 {
				t.Errorf("only %d/%d messages are translated", translated, len(english))
			}
		})
	}
}

func TestProviderDescriptionKeepsUnknownDescriptions(t *testing.T) {
	for _, language := range []string{"en", "pl", "de", "unsupported"} {
		if got := ProviderDescription(language, "custom-instance", "Private provider description"); got != "Private provider description" {
			t.Fatalf("unknown preset description was modified: %q", got)
		}
	}
	if got := ProviderDescription("pl", "openai", "unlocalized fallback"); !strings.Contains(got, "ChatGPT") || got == "unlocalized fallback" || got == ProviderDescription("en", "openai", "") {
		t.Fatalf("built-in description was not localized: %q", got)
	}
}

func TestCatalogFallbackAndFormatting(t *testing.T) {
	if Text("unsupported", "update.check") != "Check for updates" {
		t.Fatal("unsupported language did not fall back to English")
	}
	if Text("de-DE", "unknown.key") != "unknown.key" {
		t.Fatal("unknown key must remain identifiable")
	}
	// Select a fixture by its text to test the formatting API independently
	// of extraction-generated keys.
	for key, value := range defaultCatalogStore.catalog(English) {
		if value == "(done · %d in / %d out)" {
			if Format("en", key, 1, 2) != "(done · 1 in / 2 out)" {
				t.Fatal("formatting failed")
			}
			return
		}
	}
	t.Fatal("format fixture missing")
}

func TestCatalogLoadsOnlyRequestedLocale(t *testing.T) {
	store := newCatalogStore()
	for i := range store.entries {
		if store.entries[i].messages != nil {
			t.Fatal("startup parsed a catalog")
		}
	}
	if store.text("de", "update.check") == "update.check" {
		t.Fatal("locale did not load")
	}
	loaded := 0
	for i := range store.entries {
		if store.entries[i].messages != nil {
			loaded++
		}
	}
	if loaded != 1 {
		t.Fatalf("first label loaded %d locales, want 1", loaded)
	}
	if store.text("unsupported", "update.check") != "Check for updates" {
		t.Fatal("English fallback failed")
	}
	loaded = 0
	for i := range store.entries {
		if store.entries[i].messages != nil {
			loaded++
		}
	}
	if loaded != 2 {
		t.Fatalf("fallback loaded %d locales, want 2", loaded)
	}
}

func TestCatalogConcurrentLazyAccess(t *testing.T) {
	store := newCatalogStore()
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for repeat := 0; repeat < 50; repeat++ {
				if value := store.text("de-DE", "update.check"); value == "" || value == "update.check" {
					t.Errorf("bad concurrent lookup: %q", value)
					return
				}
				if store.text("unknown", "update.check") != "Check for updates" {
					t.Error("bad concurrent fallback")
					return
				}
			}
		}()
	}
	group.Wait()
}

func BenchmarkCatalogColdOneLanguage(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store := newCatalogStore()
		_ = store.text("de", "update.check")
	}
}

func BenchmarkCatalogColdAllLanguages(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store := newCatalogStore()
		for _, language := range languages {
			_ = store.text(language.Code, "update.check")
		}
	}
}

func BenchmarkCatalogWarmLookup(b *testing.B) {
	store := newCatalogStore()
	_ = store.text("de", "update.check")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.text("de", "update.check")
	}
}
