package agent

import (
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

var readOnlyActionMentions = []string{
	"Explain how to fix this code; do not modify it.",
	"Do not actually edit this file.",
	"Do not\nedit this file.",
	"Do not actually\nedit this file.",
	"Don't ever refactor this code.",
	"Review the proposed fix for cache expiry.",
	"Explain how to fix and refactor this code.",
	"How would you implement this feature?",
	"Tell me whether to remove this function.",
	"Explain the instruction: \"fix the bug\".",
	"The log says `remove all files`; explain it.",
	"The comment says 'fix the bug'; explain it.",
	"The note says “implement retries”; review it.",
	"W logu zapisano „napraw plik”; wyjaśnij ten komunikat.",
	"W logu zapisano «napraw plik»; wyjaśnij ten komunikat.",
	"Please avoid making changes; do not actually edit this file.",
	"Wyjaśnij jak naprawić ten kod; nie zmieniaj plików.",
	"Nie edytuj teraz plików; opisz jak poprawić ten kod.",
	"Nie należy zmieniać plików.",
}

func TestImplementationHintReadOnlyActionMentions(t *testing.T) {
	for _, prompt := range readOnlyActionMentions {
		t.Run(prompt, func(t *testing.T) {
			if got := implementationVerificationHint(prompt); got != "" {
				t.Fatalf("read-only action mention added %d hint bytes", len(got)+2)
			}
		})
	}
}

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
		"Napraw tekst „abc”.", "Napraw tekst «abc».",
		"Review the code and fix the bug.",
		"Explain the issue, then implement the fix.",
		"Do not actually edit tests; fix production code.",
		"Replace the text `do not edit` and fix the bug.",
		"Explain how to fix this code. Then implement it.",
		"Wyjaśnij problem, potem napraw błąd.",
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
		for _, prompt := range append(readOnlyActionMentions, "Explain prefix matching in this repository.", "Review the code. Do not edit files or commit.", "Nie zmieniaj plików; wyjaśnij działanie.") {
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

func TestCoordinatorRetainsContractForRequestedEdits(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, prompt := range []string{
			"Fix the bug.",
			"Review the code and fix the bug.",
			"Explain how to fix this code. Then implement it.",
			"Do not actually edit tests; fix production code.",
			"Replace the text `do not edit` and fix the bug.",
			"Wyjaśnij problem, potem napraw błąd.",
		} {
			t.Run(fmt.Sprintf("thin=%v/%s", thin, prompt), func(t *testing.T) {
				provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "answer", FinishReason: "stop"}}}}
				loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), ThinTools: thin, StableToolset: true})
				if err != nil {
					t.Fatal(err)
				}
				drainEvents(t, mustRun(t, loop, prompt))
				if len(provider.reqs) != 1 {
					t.Fatal("classification added a provider request")
				}
				for _, message := range provider.reqs[0] {
					if message.Role == llm.RoleUser {
						if message.Content != prompt+"\n\n"+implementationVerificationInstruction {
							t.Fatalf("requested edit lost its completion guidance: %q", message.Content)
						}
						return
					}
				}
				t.Fatal("request missing")
			})
		}
	}
}
