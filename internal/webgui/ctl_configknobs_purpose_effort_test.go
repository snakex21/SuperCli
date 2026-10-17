package webgui

import (
	"net/http"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestPurposeEffortKnobs(t *testing.T) {
	initial := llm.ReasoningEffort()
	t.Cleanup(func() { _ = llm.SetReasoningEffort(initial) })
	_ = llm.SetReasoningEffort("medium")
	for _, key := range []string{"advisor_reasoning_effort", "review_reasoning_effort", "darwin_reasoning_effort"} {
		t.Run(key, func(t *testing.T) {
			srv := newTestServer(t, false)
			for _, value := range []string{"max", "none", " HIGH ", "default", ""} {
				rec := knobsPOST(t, srv, `{"key":"`+key+`","value":"`+value+`"}`)
				if rec.Code != http.StatusOK {
					t.Fatalf("save %q: %d %s", value, rec.Code, rec.Body.String())
				}
				want := value
				if value == " HIGH " {
					want = "high"
				}
				if value == "default" {
					want = ""
				}
				row := findKnob(t, knobsGET(t, srv), key)
				if row.Raw != want {
					t.Fatalf("raw=%q want=%q", row.Raw, want)
				}
			}
			rec := knobsPOST(t, srv, `{"key":"`+key+`","value":"bogus"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatal("invalid effort accepted")
			}
			c := config.TomlConfig{ReasoningEffort: "medium"}
			if err := knobSet(&c, key, "high"); err != nil {
				t.Fatal(err)
			}
			if c.ReasoningEffort != "medium" || llm.ReasoningEffort() != "medium" {
				t.Fatal("global changed")
			}
		})
	}
}
