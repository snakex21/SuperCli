package llm

import (
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

var (
	tokenizerOnce sync.Once
	gptTokenizer  *tiktoken.Tiktoken
	tokenizerErr  error

	// QwenPatterns: model IDs that use chars/3 estimation.
	qwenPatterns = []string{"qwen", "llama", "mistral", "mixtral", "gemma", "phi"}
)

// CountTokens returns the number of tokens in text for the
// given model ID. Uses tiktoken-go for GPT-family models,
// chars/3 for Qwen/Llama (more tokens per char), chars/4
// as universal fallback.
func CountTokens(text, model string) int {
	if text == "" {
		return 0
	}

	// Try tiktoken-go for GPT-family models.
	if isGPTFamily(model) {
		tk, err := getGPTTokenizer()
		if err == nil {
			return len(tk.Encode(text, nil, nil))
		}
	}

	// Qwen/Llama: chars/3 is more accurate.
	ratio := 4
	for _, pat := range qwenPatterns {
		if strings.Contains(strings.ToLower(model), pat) {
			ratio = 3
			break
		}
	}
	return (len(text) + ratio - 1) / ratio
}

// CountTokensEstimate is like CountTokens but always returns
// an estimate (never 0 for non-empty text).
func CountTokensEstimate(text, model string) (int, bool) {
	if text == "" {
		return 0, true
	}
	tk, err := getGPTTokenizer()
	if err == nil && isGPTFamily(model) {
		return len(tk.Encode(text, nil, nil)), false // exact count
	}
	// Fallback estimation.
	ratio := 4
	for _, pat := range qwenPatterns {
		if strings.Contains(strings.ToLower(model), pat) {
			ratio = 3
			break
		}
	}
	return (len(text) + ratio - 1) / ratio, true // estimated
}

func isGPTFamily(model string) bool {
	lower := strings.ToLower(model)
	gptPatterns := []string{"gpt", "o1", "o3", "o4", "claude", "gemini"}
	for _, pat := range gptPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func getGPTTokenizer() (*tiktoken.Tiktoken, error) {
	tokenizerOnce.Do(func() {
		gptTokenizer, tokenizerErr = tiktoken.GetEncoding("cl100k_base")
	})
	return gptTokenizer, tokenizerErr
}
