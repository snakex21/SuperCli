package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func legacySingleDataSSE(r io.Reader, onEvent func(eventName, data string) error) error {
	scanner := bufio.NewScanner(r)
	// Some SSE payloads (especially the [DONE] terminator plus one
	// large final chunk) exceed the default 64 KiB token size.
	// 1 MiB is plenty for chat-completion chunks.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		event string
		data  strings.Builder
	)
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		err := onEvent(event, data.String())
		event = ""
		data.Reset()
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			// comment / heartbeat, ignore
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			// field with no value; treat as field with empty value
			continue
		}
		field := line[:colon]
		value := line[colon+1:]
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			event = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		case "id", "retry":
			// last-event-id and retry intervals are not relevant
			// for the OpenAI chat-completions stream; ignore.
		default:
			// unknown field, ignore
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// Trailing event without a blank line terminator.
	if err := flush(); err != nil {
		return err
	}
	return nil
}

type sseCopyPair struct{ Event, Data string }
type sseCopyFailReader struct{}

func (sseCopyFailReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type sseCopyByteReader struct{ io.Reader }

func (r sseCopyByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}
func collectR13SSE(parse func(io.Reader, func(string, string) error) error, r io.Reader, stop int) ([]sseCopyPair, string) {
	var out []sseCopyPair
	err := parse(r, func(e, d string) error {
		out = append(out, sseCopyPair{e, d})
		if stop > 0 && len(out) == stop {
			return errors.New("callback stop")
		}
		return nil
	})
	if err != nil {
		return out, err.Error()
	}
	return out, ""
}
func TestSSESingleLineExactOracle(t *testing.T) {
	inputs := []string{"", "event: pending\ndata:\n\nevent: later\ndata: value\n\n", "data: first\ndata:\ndata: third\n\n", "data: \ndata:  \ndata:\n", "event: old\ndata: first\nevent: new\ndata: second\n\n", "data: \x00\xff\xfe\n\n", "event: źródło\r\ndata: ż界😀\r\n\r\n", "data: first\n\ndata: last", "data:" + strings.Repeat("x", 1024*1024) + "\n"}
	rng := rand.New(rand.NewSource(731))
	fields := []string{"event: delta", "event: alternate", "event:", "data: first", "data: ", "data:", "data: second  ", "data: ż界😀", "data: \x00\xff", ": heartbeat", "unknown: ignored", "id: 3", "retry: 1", "withoutcolon", ""}
	for n := 0; n < 300; n++ {
		var lines []string
		for i := 0; i < 1+rng.Intn(40); i++ {
			lines = append(lines, fields[rng.Intn(len(fields))])
		}
		inputs = append(inputs, strings.Join(lines, "\n"))
	}
	for i, input := range inputs {
		for _, mode := range []string{"normal", "byte", "error", "stop"} {
			var oldReader, newReader io.Reader = strings.NewReader(input), strings.NewReader(input)
			stop := 0
			switch mode {
			case "byte":
				oldReader = sseCopyByteReader{oldReader}
				newReader = sseCopyByteReader{newReader}
			case "error":
				oldReader = io.MultiReader(oldReader, sseCopyFailReader{})
				newReader = io.MultiReader(newReader, sseCopyFailReader{})
			case "stop":
				stop = 1
			}
			want, werr := collectR13SSE(legacySingleDataSSE, oldReader, stop)
			got, gerr := collectR13SSE(parseSSE, newReader, stop)
			if !reflect.DeepEqual(want, got) || werr != gerr {
				t.Fatalf("case %d/%s differs: want %#v %q, got %#v %q", i, mode, want, werr, got, gerr)
			}
		}
	}
}
func TestSSECallbacksOwnStrings(t *testing.T) {
	var s strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&s, "event: event-%d\ndata: %s\n\n", i, strings.Repeat(fmt.Sprintf("value-%d", i), 1000))
	}
	got, err := collectR13SSE(parseSSE, strings.NewReader(s.String()), 0)
	if err != "" || len(got) != 80 {
		t.Fatal(err, len(got))
	}
	for i, p := range got {
		if p.Event != fmt.Sprintf("event-%d", i) || p.Data != strings.Repeat(fmt.Sprintf("value-%d", i), 1000) {
			t.Fatalf("callback %d changed after later scan", i)
		}
	}
}

type sseCopyRoundTrip func(*http.Request) (*http.Response, error)

func (f sseCopyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func sseCopyAnthropicFixture(bytes, chunks int) string {
	var wire strings.Builder
	for i := 0; i < chunks; i++ {
		payload, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": strings.Repeat("x", bytes)}})
		fmt.Fprintf(&wire, "event: content_block_delta\ndata: %s\n\n", payload)
	}
	wire.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return wire.String()
}
func BenchmarkSSESingleLineFrames(b *testing.B) {
	for _, c := range []struct {
		name          string
		bytes, chunks int
	}{{"small128", 8, 128}, {"4k64", 4096, 64}, {"64k8", 65536, 8}} {
		b.Run(c.name, func(b *testing.B) {
			wire := sseCopyAnthropicFixture(c.bytes, c.chunks)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				n := 0
				err := parseSSE(strings.NewReader(wire), func(_, d string) error { n += len(d); return nil })
				if err != nil || n == 0 {
					b.Fatal(err, n)
				}
			}
		})
	}
}
func BenchmarkAnthropicSingleLineComplete(b *testing.B) {
	for _, c := range []struct {
		name          string
		bytes, chunks int
	}{{"small128", 8, 128}, {"4k64", 4096, 64}, {"64k8", 65536, 8}} {
		b.Run(c.name, func(b *testing.B) {
			wire := sseCopyAnthropicFixture(c.bytes, c.chunks)
			transport := sseCopyRoundTrip(func(r *http.Request) (*http.Response, error) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire)), Request: r}, nil
			})
			p, err := NewAnthropic(AnthropicConfig{BaseURL: "https://fixture.invalid/v1", APIKey: "fixture", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				b.Fatal(err)
			}
			messages := []Message{{Role: RoleUser, Content: "synthetic stream"}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				ch, err := p.Complete(context.Background(), messages, nil)
				if err != nil {
					b.Fatal(err)
				}
				n := 0
				for d := range ch {
					if d.Err != nil {
						b.Fatal(d.Err)
					}
					n += len(d.Content)
				}
				if n != c.bytes*c.chunks {
					b.Fatal(n)
				}
			}
		})
	}
}

func BenchmarkAnthropicMultilineComplete(b *testing.B) {
	wire := strings.ReplaceAll(sseCopyAnthropicFixture(4096, 64), ",", ",\ndata: ")
	transport := sseCopyRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire)), Request: r}, nil
	})
	p, err := NewAnthropic(AnthropicConfig{BaseURL: "https://fixture.invalid/v1", APIKey: "fixture", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		b.Fatal(err)
	}
	messages := []Message{{Role: RoleUser, Content: "synthetic multiline stream"}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ch, err := p.Complete(context.Background(), messages, nil)
		if err != nil {
			b.Fatal(err)
		}
		n := 0
		for d := range ch {
			if d.Err != nil {
				b.Fatal(d.Err)
			}
			n += len(d.Content)
		}
		if n != 64*4096 {
			b.Fatal(n)
		}
	}
}
