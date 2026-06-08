package llm

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseSSE_SingleEvent(t *testing.T) {
	input := "data: hello\n\n"
	var got []string
	err := parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, event+"|"+data)
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 1 || got[0] != "|hello" {
		t.Fatalf("got %v, want [|hello]", got)
	}
}

func TestParseSSE_EventName(t *testing.T) {
	input := "event: ping\ndata: payload\n\n"
	var got []string
	_ = parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, event+"|"+data)
		return nil
	})
	if len(got) != 1 || got[0] != "ping|payload" {
		t.Fatalf("got %v, want [ping|payload]", got)
	}
}

func TestParseSSE_MultilineData(t *testing.T) {
	input := "data: line1\ndata: line2\ndata: line3\n\n"
	var got []string
	_ = parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, data)
		return nil
	})
	if len(got) != 1 || got[0] != "line1\nline2\nline3" {
		t.Fatalf("got %v", got)
	}
}

func TestParseSSE_MultipleEvents(t *testing.T) {
	input := "data: a\n\ndata: b\n\ndata: c\n\n"
	var got []string
	_ = parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, data)
		return nil
	})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %v, want [a b c]", got)
	}
}

func TestParseSSE_CommentsIgnored(t *testing.T) {
	input := ": heartbeat\n: another\ndata: real\n\n"
	var got []string
	_ = parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, data)
		return nil
	})
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("got %v, want [real]", got)
	}
}

func TestParseSSE_TrailingNoBlankLine(t *testing.T) {
	input := "data: noblank"
	var got []string
	_ = parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, data)
		return nil
	})
	if len(got) != 1 || got[0] != "noblank" {
		t.Fatalf("got %v, want [noblank]", got)
	}
}

func TestParseSSE_OnEventError(t *testing.T) {
	sentinel := errors.New("stop")
	input := "data: a\n\ndata: b\n\n"
	calls := 0
	err := parseSSE(strings.NewReader(input), func(event, data string) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestParseSSE_EmptyStream(t *testing.T) {
	calls := 0
	err := parseSSE(strings.NewReader(""), func(event, data string) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestParseSSE_LongData(t *testing.T) {
	// 100 KiB single data line - must not exceed scanner buffer.
	payload := strings.Repeat("x", 100*1024)
	input := "data: " + payload + "\n\n"
	var got []string
	err := parseSSE(strings.NewReader(input), func(event, data string) error {
		got = append(got, data)
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 1 || got[0] != payload {
		t.Fatalf("payload length = %d, want %d", len(got[0]), len(payload))
	}
}

func TestParseSSE_ReaderError(t *testing.T) {
	r := io.MultiReader(strings.NewReader("data: a\n\n"), errReader{})
	err := parseSSE(r, func(event, data string) error { return nil })
	if err == nil {
		t.Fatal("expected error from underlying reader")
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errors.New("boom") }

func TestIsDone(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"[DONE]", true},
		{"  [DONE]  ", true},
		{"[done]", false},
		{"", false},
		{"actual data", false},
	}
	for _, c := range cases {
		if got := isDone(c.in); got != c.want {
			t.Fatalf("isDone(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
