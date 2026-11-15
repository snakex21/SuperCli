package llm

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Some catalogs publish per-model tool support (Logfare's
// chat_capabilities.tools, OpenRouter's supported_parameters). A live test of
// Logfare (1.0.28) showed that a model listed with tools:false does NOT
// reject the tools field: it answers 200, ignores the tools and writes
// unrelated text (2048 tokens of a recipe for "read notes.txt"). The 400
// fallback in openai_tools_unsupported.go never fires for it. So before the
// first request with tools to a remote custom endpoint, SuperCli reads that
// endpoint's /models once per process; a model the catalog explicitly lists
// without tool support is sent without tools, with the same notice as the
// 400 path. Catalogs without that metadata, failures and loopback hosts
// (local servers, tests) change nothing.

const catalogToolsTimeout = 4 * time.Second

type catalogToolsEntry struct {
	once    sync.Once
	noTools map[string]bool // model id (lower-case) -> listed without tools
}

var catalogTools sync.Map // base URL -> *catalogToolsEntry

// catalogToolsSkipLocal is false only in tests (httptest servers are loopback).
var catalogToolsSkipLocal = true

// catalogToolsFetch is replaced in tests.
var catalogToolsFetch = func(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, error) {
	return ListProviderModelInfos(ctx, baseURL, apiKey)
}

// catalogListsWithoutTools reports whether the endpoint's model catalog
// explicitly lists model without tool support.
func catalogListsWithoutTools(ctx context.Context, baseURL, apiKey, model string) bool {
	// OpenCode Zen has its own catalog and request path (zen_*.go); never
	// send it an extra /models request.
	if model == "" || (catalogToolsSkipLocal && isLocalBaseURL(baseURL)) || firstPartyToolsHost(baseURL) || isOpenCodeZenBaseURL(baseURL) {
		return false
	}
	v, _ := catalogTools.LoadOrStore(strings.TrimRight(baseURL, "/"), &catalogToolsEntry{})
	entry := v.(*catalogToolsEntry)
	entry.once.Do(func() {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), catalogToolsTimeout)
		defer cancel()
		models, err := catalogToolsFetch(fetchCtx, baseURL, apiKey)
		if err != nil {
			return
		}
		entry.noTools = map[string]bool{}
		for _, m := range models {
			if m.ToolUseKnown && !m.ToolUse {
				entry.noTools[strings.ToLower(m.ID)] = true
			}
		}
	})
	return entry.noTools[strings.ToLower(model)]
}

// firstPartyToolsHost skips hosts whose chat models all call tools and whose
// catalogs carry no capability metadata.
func firstPartyToolsHost(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.openai.com", "api.anthropic.com":
		return true
	}
	return false
}

func resetCatalogTools() {
	catalogTools.Range(func(k, _ any) bool {
		catalogTools.Delete(k)
		return true
	})
}
