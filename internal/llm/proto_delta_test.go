package llm

import "testing"

type sentinelErr string

func (e sentinelErr) Error() string { return string(e) }

var errSentinel sentinelErr = "boom"

func TestDelta_Validate(t *testing.T) {
	cases := []struct {
		name    string
		delta   Delta
		wantErr bool
	}{
		{"empty ok", Delta{}, false},
		{"role ok", Delta{Role: RoleAssistant, Content: "x"}, false},
		{"role bad", Delta{Role: Role("wizard")}, true},
		{"content and tool both set", Delta{Content: "x", ToolCall: &ToolCall{}}, true},
		{"tool only ok", Delta{ToolCall: &ToolCall{ID: "1"}}, false},
		{"err ok", Delta{Err: errSentinel}, false},
		{"finish ok", Delta{FinishReason: "stop"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.delta.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestDelta_IsTerminal(t *testing.T) {
	if (Delta{Content: "x"}).IsTerminal() {
		t.Fatal("content-only delta is not terminal")
	}
	if !(Delta{FinishReason: "stop"}).IsTerminal() {
		t.Fatal("finish_reason delta is terminal")
	}
	if !(Delta{Err: errSentinel}).IsTerminal() {
		t.Fatal("err delta is terminal")
	}
}

func TestToolCall_Accumulates(t *testing.T) {
	tc := ToolCall{ID: "call_1", Name: "search", Arguments: `{"query":"`}
	tc.Arguments += `test"}`
	if tc.ID != "call_1" {
		t.Errorf("ID = %q", tc.ID)
	}
	if tc.Name != "search" {
		t.Errorf("Name = %q", tc.Name)
	}
	if tc.Arguments != `{"query":"test"}` {
		t.Errorf("Arguments = %q", tc.Arguments)
	}
}

func TestToolCall_ZeroValue(t *testing.T) {
	var tc ToolCall
	if tc.ID != "" || tc.Name != "" || tc.Arguments != "" {
		t.Error("zero ToolCall should have empty fields")
	}
}

func TestUsage_ZeroValue(t *testing.T) {
	var u Usage
	if u.Input != 0 || u.Output != 0 || u.Total != 0 {
		t.Error("zero Usage should have all fields 0")
	}
}

func TestUsage_Values(t *testing.T) {
	u := Usage{Input: 100, Output: 50, Total: 150}
	if u.Input != 100 || u.Output != 50 || u.Total != 150 {
		t.Error("Usage fields not preserved")
	}
}

func TestDelta_UsageOnTerminal(t *testing.T) {
	u := &Usage{Input: 10, Output: 20, Total: 30}
	d := Delta{FinishReason: "stop", Usage: u}
	if !d.IsTerminal() {
		t.Fatal("should be terminal")
	}
	if d.Usage != u {
		t.Fatal("usage not preserved")
	}
	if d.Usage.Input != 10 {
		t.Errorf("usage.Input = %d", d.Usage.Input)
	}
}
