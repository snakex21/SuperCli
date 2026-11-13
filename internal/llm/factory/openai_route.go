package factory

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// OpenAIReasoningResponsesEnv overrides the config.toml switch
// openai_reasoning_responses (1/true/on or 0/false/off).
const OpenAIReasoningResponsesEnv = "SUPERCLI_OPENAI_REASONING_RESPONSES"

const officialOpenAIBaseURL = "https://api.openai.com/v1"

// UseResponsesForOpenAI reports whether a provider of type "openai" is built
// on the Responses API instead of Chat Completions. Only official OpenAI
// reasoning models (gpt-5*, o1/o3/o4*, codex*) on api.openai.com qualify:
// Chat Completions rejects function tools together with reasoning_effort for
// them, so the chat path silently ran without the selected reasoning level.
// Gateways, Azure, local servers and classic chat models keep Chat
// Completions. On by default; openai_reasoning_responses = false in the
// portable config.toml (or the environment override) restores the chat path.
func UseResponsesForOpenAI(cfg config.Config, dataDir string) bool {
	if cfg.Provider != config.ProviderOpenAI || !isOfficialOpenAIBaseURL(cfg.BaseURL) || !llm.IsOpenAIReasoningModel(cfg.Model) {
		return false
	}
	return openAIReasoningResponsesEnabled(dataDir)
}

// OpenAIResponsesBaseURL is the API root for a routed official OpenAI model.
// An empty base URL means the OpenAI default, as for the chat transport.
func OpenAIResponsesBaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return officialOpenAIBaseURL
	}
	return strings.TrimSuffix(raw, "/chat/completions")
}

func isOfficialOpenAIBaseURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true // the OpenAI transport defaults to api.openai.com
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}

func openAIReasoningResponsesEnabled(dataDir string) bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(OpenAIReasoningResponsesEnv))); v != "" {
		switch v {
		case "on", "yes":
			return true
		case "off", "no":
			return false
		}
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	if strings.TrimSpace(dataDir) == "" {
		return true
	}
	tc, err := config.LoadToml(filepath.Join(dataDir, "config.toml"))
	if err != nil || tc.OpenAIReasoningResponses == nil {
		return true
	}
	return *tc.OpenAIReasoningResponses
}
