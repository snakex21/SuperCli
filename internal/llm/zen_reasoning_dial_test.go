package llm

import (
	"encoding/json"
	"testing"
)

func TestZenPrepareHonorsReasoningDial(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	raw := `{"model":"m","input":[],"tools":[{"type":"function","name":"bash","parameters":{"type":"object"}}],"stream":true,"store":false,"include":[]}`

	if err := SetReasoningEffort(""); err != nil {
		t.Fatal(err)
	}
	body, err := prepareOpenCodeZenResponsesRequest([]byte(raw), "ses_0123456789abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	var unset map[string]any
	if err := json.Unmarshal(body, &unset); err != nil {
		t.Fatal(err)
	}
	if _, ok := unset["reasoning"]; ok {
		t.Fatalf("unset dial must omit reasoning: %#v", unset["reasoning"])
	}

	if err := SetReasoningEffort("xhigh"); err != nil {
		t.Fatal(err)
	}
	body, err = prepareOpenCodeZenResponsesRequest([]byte(raw), "ses_0123456789abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	var xh map[string]any
	if err := json.Unmarshal(body, &xh); err != nil {
		t.Fatal(err)
	}
	r, _ := xh["reasoning"].(map[string]any)
	if r == nil || r["effort"] != "xhigh" {
		t.Fatalf("xhigh dial not sent: %#v", xh["reasoning"])
	}
}
