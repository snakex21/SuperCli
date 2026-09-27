package agent

import (
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestImplementationHintWordBoundariesAndNegation(t *testing.T) {
	for _, prompt := range []string{
		"Explain prefix matching in this repository.",
		"What are the credit limits?",
		"Explain this unimplemented function.",
		"Describe user_refactor configuration.",
		"Explain żedit syntax and 3fix identifiers.",
		"Do not edit files or commit.",
		"Don't build anything; just explain the configuration.",
		"Don’t refactor anything.",
		"Never remove files.",
		"Nie poprawiaj plików. Opisz problem.",
		"Nie zmieniaj testów; tylko je przeczytaj.",
		"Przejrzyj to bez poprawiania plików.",
	} {
		t.Run(prompt, func(t *testing.T) {
			if hint := implementationVerificationHint(prompt); hint != "" {
				t.Fatalf("non-action request received implementation contract (%d added bytes)", len(hint)+2)
			}
		})
	}
	for _, prompt := range []string{
		"Fix the bug.", "Please fix: the cache expiry.", "Napraw błąd.", "Popraw funkcję.",
		"Do not edit tests; edit production code.",
		"Nie zmieniaj testów; zmień obsługę błędu.",
		"Do not forget to fix the bug.",
		"Don't just explain; implement the fix.",
		"Rebuild the application.", "Reimplement the parser.",
		"I think not. Fix the bug.", "(popraw ten błąd)",
	} {
		t.Run(prompt, func(t *testing.T) {
			if hint := implementationVerificationHint(prompt); hint == "" {
				t.Fatal("explicit action lost implementation contract")
			}
		})
	}
}

func TestCoordinatorDoesNotInjectContractForNonActions(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, prompt := range []string{"Explain prefix matching in this repository.", "Review the code. Do not edit files or commit.", "Nie zmieniaj plików; wyjaśnij działanie."} {
			t.Run(fmt.Sprintf("thin=%v/%s", thin, prompt), func(t *testing.T) {
				provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "answer", FinishReason: "stop"}}}}
				loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), ThinTools: thin, StableToolset: true})
				if err != nil {
					t.Fatal(err)
				}
				drainEvents(t, mustRun(t, loop, prompt))
				if provider.calls != 1 || len(provider.reqs) != 1 {
					t.Fatal("classification added a provider request")
				}
				seen := false
				for _, message := range provider.reqs[0] {
					if strings.Contains(message.Content, implementationVerificationInstruction) {
						t.Fatal("unwanted implementation instruction reached provider")
					}
					if message.Role == llm.RoleUser {
						seen = true
						if message.Content != prompt {
							t.Fatalf("user request changed: %q", message.Content)
						}
					}
				}
				if !seen {
					t.Fatal("request missing")
				}
			})
		}
	}
}
