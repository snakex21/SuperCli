package webgui

import (
	"encoding/json"
	"strings"
	"supercli/internal/tools"
	"testing"
)

func TestQuestionWireTrustedLocalizationOnly(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		e := &Engine{questions: make(map[string]tools.AskRequest)}
		req := tools.AskRequest{ID: "localized", Question: "raw fallback", Confirmation: trusted, QuestionKey: "hostPower.confirm", QuestionArgs: map[string]string{"goal": "<script> C:/long/path"}, Options: []tools.AskOption{{Label: "Cancel"}, {Label: "Allow once"}}}
		out := e.registerQuestion(req)
		if out.Question != "raw fallback" || out.Confirmation != trusted {
			t.Fatalf("wire=%+v", out)
		}
		wire, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if trusted {
			if out.QuestionKey != "hostPower.confirm" || out.QuestionArgs["goal"] != req.QuestionArgs["goal"] {
				t.Fatalf("missing metadata: %s", wire)
			}
			if !strings.Contains(string(wire), `"question_key"`) || !strings.Contains(string(wire), `"question_args"`) {
				t.Fatalf("wire=%s", wire)
			}
		} else if out.QuestionKey != "" || len(out.QuestionArgs) != 0 || strings.Contains(string(wire), `"question_key"`) {
			t.Fatalf("untrusted metadata leaked: %s", wire)
		}
	}
}
