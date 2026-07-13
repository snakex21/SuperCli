package interactive

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// helper: a buffered input channel with no consumer; the tool
// will block on send and the test sets a deadline.
func newTestAskUser(buffer int) (*AskUser, chan AskRequest) {
	ch := make(chan AskRequest, buffer)
	return NewAskUser(ch), ch
}

func TestAskUser_Spec(t *testing.T) {
	a, _ := newTestAskUser(0)
	spec := a.Spec()
	if spec.Name != "ask_user" {
		t.Fatalf("Name = %q", spec.Name)
	}
	if !strings.Contains(spec.Description, "instead of guessing") {
		t.Fatal("Description should mention 'instead of guessing'")
	}
	if spec.Fn == nil {
		t.Fatal("Fn is nil")
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestAskUser_RegistersInRegistry(t *testing.T) {
	a, _ := newTestAskUser(1)
	r := NewRegistry()
	if err := r.Register(a.Spec()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !strings.Contains(strings.Join(r.Names(), ","), "ask_user") {
		t.Fatalf("registry missing ask_user: %v", r.Names())
	}
}

func TestAskUser_Execute_HappyPath(t *testing.T) {
	a, ch := newTestAskUser(1)
	args := json.RawMessage(`{
		"question":"Which DB?",
		"header":"DB",
		"options":[
			{"label":"SQLite","description":"no CGO"},
			{"label":"Postgres","description":"requires lib/pq"}
		]
	}`)

	// Consumer answers after receiving the request.
	answered := make(chan struct{})
	go func() {
		req := <-ch
		req.Respond <- AskAnswer{Selected: []string{"SQLite"}, MultiSelect: false}
		close(answered)
	}()

	res, err := a.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err = %v", res.Err)
	}
	if !strings.Contains(res.Text, "SQLite") {
		t.Fatalf("Text = %q, want contains 'SQLite'", res.Text)
	}
	<-answered
}

func TestAskUser_Execute_MultiSelect(t *testing.T) {
	a, ch := newTestAskUser(1)
	args := json.RawMessage(`{
		"question":"Pick formats",
		"options":[
			{"label":"JSON"},
			{"label":"YAML"},
			{"label":"TOML"}
		],
		"multiSelect": true
	}`)

	go func() {
		req := <-ch
		req.Respond <- AskAnswer{
			Selected:    []string{"JSON", "YAML"},
			MultiSelect: true,
		}
	}()

	res, err := a.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "JSON") || !strings.Contains(res.Text, "YAML") {
		t.Fatalf("Text = %q, want both labels", res.Text)
	}
}

func TestAskUser_Execute_Cancelled(t *testing.T) {
	a, ch := newTestAskUser(1)
	args := json.RawMessage(`{
		"question":"Continue?",
		"options":[{"label":"Yes"},{"label":"No"}]
	}`)

	go func() {
		req := <-ch
		req.Respond <- AskAnswer{Cancelled: true}
	}()

	res, err := a.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "cancelled") {
		t.Fatalf("Text = %q, want 'cancelled'", res.Text)
	}
}

func TestAskUser_Execute_EmptySelection(t *testing.T) {
	a, ch := newTestAskUser(1)
	args := json.RawMessage(`{
		"question":"x","options":[{"label":"A"},{"label":"B"}]
	}`)

	go func() {
		req := <-ch
		req.Respond <- AskAnswer{Selected: nil, MultiSelect: true}
	}()

	res, err := a.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "did not pick") {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestAskUser_Execute_ContextCancel(t *testing.T) {
	a, _ := newTestAskUser(0) // unbuffered; will block on send
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	args := json.RawMessage(`{"question":"x","options":[{"label":"A"},{"label":"B"}]}`)
	_, err := a.Execute(ctx, args)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestAskUser_Execute_Timeout(t *testing.T) {
	a, _ := newTestAskUser(1)
	a.Timeout = 50 * time.Millisecond
	// Do NOT consume from ch; the tool will time out.
	args := json.RawMessage(`{"question":"x","options":[{"label":"A"},{"label":"B"}]}`)
	start := time.Now()
	_, err := a.Execute(context.Background(), args)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout took %v, want ~50ms", elapsed)
	}
}

func TestAskUser_Execute_BadJSON(t *testing.T) {
	a, _ := newTestAskUser(1)
	_, err := a.Execute(context.Background(), json.RawMessage("not json"))
	if err == nil {
		t.Fatal("expected error on bad json")
	}
}

// --- param validation ---

func TestAskParams_Validate(t *testing.T) {
	cases := []struct {
		name    string
		params  askParams
		wantErr bool
	}{
		{
			"ok 2 options",
			askParams{Question: "x", Options: []AskOption{{Label: "A"}, {Label: "B"}}},
			false,
		},
		{
			"ok 4 options",
			askParams{Question: "x", Options: []AskOption{{Label: "A"}, {Label: "B"}, {Label: "C"}, {Label: "D"}}},
			false,
		},
		{
			"empty question",
			askParams{Options: []AskOption{{Label: "A"}, {Label: "B"}}},
			true,
		},
		{
			"1 option",
			askParams{Question: "x", Options: []AskOption{{Label: "A"}}},
			true,
		},
		{
			"5 options",
			askParams{Question: "x", Options: []AskOption{{Label: "A"}, {Label: "B"}, {Label: "C"}, {Label: "D"}, {Label: "E"}}},
			true,
		},
		{
			"header too long",
			askParams{Question: "x", Header: "this is way too long", Options: []AskOption{{Label: "A"}, {Label: "B"}}},
			true,
		},
		{
			"empty label",
			askParams{Question: "x", Options: []AskOption{{Label: ""}, {Label: "B"}}},
			true,
		},
		{
			"ok with header 12",
			askParams{Question: "x", Header: "123456789012", Options: []AskOption{{Label: "A"}, {Label: "B"}}},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.params.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

// --- format helpers ---

func TestFormatAskAnswer(t *testing.T) {
	cases := []struct {
		name string
		in   AskAnswer
		want string
	}{
		{"cancelled", AskAnswer{Cancelled: true}, "cancelled"},
		{"none picked", AskAnswer{MultiSelect: true}, "did not pick"},
		{"single", AskAnswer{Selected: []string{"X"}}, "user selected: X"},
		{"multi", AskAnswer{Selected: []string{"X", "Y"}, MultiSelect: true}, "user selected: X, Y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatAskAnswer(c.in)
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q, want contains %q", got, c.want)
			}
		})
	}
}

func TestAskUser_CustomAnswerAndMultipleQuestions(t *testing.T) {
	ch := make(chan AskRequest, 2)
	tool := NewAskUser(ch)
	tool.Timeout = time.Second
	done := make(chan Result, 1)
	go func() {
		res, _ := tool.Execute(context.Background(), json.RawMessage(`{"questions":[
			{"question":"Layout?","options":[{"label":"A"},{"label":"B"}]},
			{"question":"Colors?","options":[{"label":"Warm"},{"label":"Cool"}],"multiSelect":true}
		]}`))
		done <- res
	}()
	first := <-ch
	if !first.AllowCustom || first.Question != "Layout?" {
		t.Fatalf("first request = %+v", first)
	}
	first.Respond <- AskAnswer{Custom: "A, but with a narrower sidebar"}
	second := <-ch
	second.Respond <- AskAnswer{Selected: []string{"Warm", "Cool"}, MultiSelect: true}
	res := <-done
	if res.Err != nil || !strings.Contains(res.Text, "narrower sidebar") || !strings.Contains(res.Text, "Warm, Cool") {
		t.Fatalf("multi answer = %+v", res)
	}
}

func TestJoinLabels(t *testing.T) {
	if got := joinLabels([]string{"a", "b", "c"}); got != "a, b, c" {
		t.Errorf("got %q", got)
	}
	if got := joinLabels([]string{"x"}); got != "x" {
		t.Errorf("got %q", got)
	}
	if got := joinLabels(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestNewAskID_Unique(t *testing.T) {
	a, b := newAskID(), newAskID()
	if a == b {
		t.Errorf("ids collide: %q vs %q", a, b)
	}
}

func TestAskUser_DefaultTimeout(t *testing.T) {
	a := NewAskUser(make(chan AskRequest, 1))
	if a.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v, want 60s", a.Timeout)
	}
}
