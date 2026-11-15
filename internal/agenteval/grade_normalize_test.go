package agenteval

import "testing"

func TestAnswerChecksAcceptTypographicApostrophes(t *testing.T) {
	c := AnswerCheck{ID: "says-missing", AnyOf: []string{`isn't (present|there|defined)`, `couldn't find`}, Regex: true}
	for _, answer := range []string{"`paginateV2` isn\u2019t present in src/lib", "I couldn\u2018t find it", `C:\x isn't there`} {
		if r := gradeAnswer(answer, c); !r.Passed {
			t.Fatalf("%q rejected: %+v", answer, r)
		}
	}
}
