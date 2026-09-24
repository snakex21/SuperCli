package interactive

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAskUserCurrentRunChannelAndCompletion(t *testing.T) {
	old := make(chan AskRequest, 1)
	current := make(chan AskRequest, 1)
	tool := NewAskUser(old)
	ctx, cancel := context.WithTimeout(WithAskChannel(context.Background(), current), time.Second)
	defer cancel()
	result := make(chan Result, 1)
	go func() {
		r, _ := tool.Execute(ctx, json.RawMessage(`{"question":"Pick","options":[{"label":"A"},{"label":"B"}]}`))
		result <- r
	}()
	var req AskRequest
	select {
	case req = <-current:
	case <-ctx.Done():
		t.Fatal("question went to stale channel")
	}
	if len(old) != 0 {
		t.Fatal("old channel received a question")
	}
	req.Respond <- AskAnswer{Selected: []string{"B"}}
	r := <-result
	if r.Err != nil || !strings.Contains(r.Text, "B") {
		t.Fatalf("result=%+v", r)
	}
	select {
	case <-req.Done:
	default:
		t.Fatal("question never closed")
	}
}

func TestAskUserDeliveryTimeoutIncludesFullQueue(t *testing.T) {
	ch := make(chan AskRequest)
	tool := NewAskUser(ch)
	tool.Timeout = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := tool.Execute(ctx, json.RawMessage(`{"question":"Pick","options":[{"label":"A"},{"label":"B"}]}`))
	if r.Err == nil || !strings.Contains(r.Err.Error(), "UI did not receive") {
		t.Fatalf("result=%+v", r)
	}
	if ctx.Err() != nil {
		t.Fatal("delivery ignored tool timeout and waited for run cancellation")
	}
}
