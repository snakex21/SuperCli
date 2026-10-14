package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// advisor_model = "off": the schema drops advise and a forced call is refused
// without starting any worker.
func TestAdvisorOffHidesAndRefusesAdvise(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	var calls atomic.Int32
	at, err := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("opinion", &calls))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(at.Spec().Schema, `"advise"`) {
		t.Fatal("advise must be offered by default")
	}
	at.AdvisorOff = true
	if strings.Contains(at.Spec().Schema, `"advise"`) {
		t.Fatal("advise must be hidden when the advisor is off")
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(at.Spec().Schema), &probe); err != nil {
		t.Fatalf("schema without advise is not valid JSON: %v", err)
	}
	res, _ := at.execute(context.Background(), json.RawMessage(`{"prompt":"A or B?","advise":true}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "turned off") {
		t.Fatalf("advise call must be refused, got %+v", res)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("no worker may start, started %d", n)
	}
}
