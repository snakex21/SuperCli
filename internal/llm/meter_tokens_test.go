package llm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pkoukk/tiktoken-go"
	"supercli/internal/system/childproc"
)

type countingTokenizerLoader struct{ calls int }

func (l *countingTokenizerLoader) LoadTiktokenBpe(string) (map[string]int, error) {
	l.calls++
	return nil, errors.New("test vocabulary unavailable")
}

// The helper process starts with fresh sync.Once and library state. Its custom
// loader prevents network/disk access and detects even an attempted vocabulary
// load; no production hooks or unsafe resets of global state are needed.
func TestTokenEstimateLoaderHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_TOKEN_LOADER_HELPER") != "1" {
		return
	}
	loader := &countingTokenizerLoader{}
	tiktoken.SetBpeLoader(loader)
	cases := []struct {
		model, text string
		want        int
	}{
		{"qwen3.8-27b-uncensored", "1234567", 3},
		{"QWEN-local", "zażółć", 4},
		{"llama-3.3", "1234567", 3},
		{"mistral-small", "1234567", 3},
		{"gemma-3", "1234567", 3},
		{"phi-4", "1234567", 3},
		{"deepseek", "1234567", 2},
		{"unknown", "1234567", 2},
		{"gpt-4", "", 0},
	}
	for _, test := range cases {
		got, estimated := CountTokensEstimate(test.text, test.model)
		if got != test.want || !estimated {
			t.Fatalf("%s: count=%d estimated=%v", test.model, got, estimated)
		}
		if direct := CountTokens(test.text, test.model); direct != test.want {
			t.Fatalf("%s: direct=%d", test.model, direct)
		}
	}
	if loader.calls != 0 {
		t.Fatalf("unused tokenizer loaded %d time(s)", loader.calls)
	}
	// Models on the tokenizer path still attempt loading once and fall back
	// honestly on failure; the earlier local counts must not poison that state.
	for i := 0; i < 2; i++ {
		got, estimated := CountTokensEstimate("1234567", "gpt-4")
		if got != 2 || !estimated {
			t.Fatalf("fallback: count=%d estimated=%v", got, estimated)
		}
	}
	if loader.calls != 1 {
		t.Fatalf("tokenizer load attempts=%d, want 1", loader.calls)
	}
}

func TestTokenEstimatesSkipUnusedTokenizer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTokenEstimateLoaderHelper$")
	cmd.Env = append(os.Environ(), "SUPERCLI_TOKEN_LOADER_HELPER=1")
	childproc.HideWindow(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, output)
	}
}
