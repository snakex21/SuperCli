package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type anthropicOwnedBodyTransport func(*http.Request) (*http.Response, error)

func (f anthropicOwnedBodyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const anthropicOwnedBodySSE = "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\",\"extra\":\"preserve\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"synthetic plan\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"fixture-signature\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\ndata: {\"type\":\"message_stop\"}\n\n"

func anthropicOwnedBodyResponse(req *http.Request, code int, text string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text)), Request: req}
}

func collectAnthropicOwnedBodyFixture(t testing.TB, p *AnthropicProvider, ctx context.Context, msgs []Message, tools []ToolDef) *ReasoningBlock {
	t.Helper()
	out, err := p.Complete(ctx, msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	var native *ReasoningBlock
	for d := range out {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
		if d.NativeReasoning != nil {
			native = d.NativeReasoning
		}
	}
	if native == nil {
		t.Fatal("missing signed native reasoning")
	}
	return native
}

func newAnthropicOwnedBodyFixture(t testing.TB, base string, transport anthropicOwnedBodyTransport) *AnthropicProvider {
	t.Helper()
	p, err := NewAnthropic(AnthropicConfig{BaseURL: base, Model: "claude-fixture", HTTPClient: &http.Client{Transport: transport}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnthropicOwnedBodyReplayRemainingParity(t *testing.T) {
	for _, text := range []string{"", "abc", strings.Repeat("xyz", 4096)} {
		for _, consume := range []int{0, 1, len(text), len(text) + 7} {
			data := []byte(text)
			own, _ := anthropicOwnedGetBody(data)()
			legacy := io.NopCloser(bytes.NewReader(data))
			buf := make([]byte, consume)
			_, _ = io.ReadFull(own, buf)
			_, _ = io.ReadFull(legacy, buf)
			got, err := anthropicReplayRequestBytes(own)
			want, werr := io.ReadAll(legacy)
			if !bytes.Equal(got, want) || !reflect.DeepEqual(err, werr) {
				t.Fatalf("size=%d consume=%d remaining mismatch", len(data), consume)
			}
			if got == nil {
				t.Fatal("ReadAll requires non-nil empty slice")
			}
			if n, e := own.Read(make([]byte, 1)); n != 0 || e != io.EOF {
				t.Fatalf("not consumed: n=%d err=%v", n, e)
			}
		}
	}
	nilOwn, _ := anthropicOwnedGetBody(nil)()
	got, err := anthropicReplayRequestBytes(nilOwn)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil bytes parity: %v %v", got, err)
	}
	first, _ := anthropicOwnedGetBody([]byte("first"))()
	second, _ := anthropicOwnedGetBody([]byte("second"))()
	a, _ := anthropicReplayRequestBytes(first)
	b, _ := anthropicReplayRequestBytes(second)
	if string(a) != "first" || string(b) != "second" {
		t.Fatal("attempt body alias")
	}
	broken := &anthropicOwnedRequestBody{reader: bytes.NewReader([]byte("real")), data: []byte("advertised")}
	got, err = anthropicReplayRequestBytes(broken)
	if err != nil || string(got) != "real" {
		t.Fatalf("size guard=%q %v", got, err)
	}
	// Internal cursor past EOF must remain past EOF, like io.ReadAll.
	for _, pos := range []int64{0, 3, 7, 20} {
		data := []byte("abcdefg")
		own, _ := anthropicOwnedGetBody(data)()
		legacy := bytes.NewReader(data)
		_, _ = own.(*anthropicOwnedRequestBody).reader.Seek(pos, io.SeekStart)
		_, _ = legacy.Seek(pos, io.SeekStart)
		got, err := anthropicReplayRequestBytes(own)
		want, werr := io.ReadAll(legacy)
		if !bytes.Equal(got, want) || err != werr {
			t.Fatalf("seek parity at %d", pos)
		}
		ownPos, _ := own.(*anthropicOwnedRequestBody).reader.Seek(0, io.SeekCurrent)
		legacyPos, _ := legacy.Seek(0, io.SeekCurrent)
		if ownPos != legacyPos {
			t.Fatalf("cursor %d vs %d", ownPos, legacyPos)
		}
	}
}

type anthropicOwnedBodyShortWriter struct{ n int }

func (w anthropicOwnedBodyShortWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		return w.n, nil
	}
	return len(p), nil
}

func TestAnthropicOwnedBodyReplayWriterToPartial(t *testing.T) {
	data := []byte("full body remains")
	own, _ := anthropicOwnedGetBody(data)()
	legacy := io.NopCloser(bytes.NewReader(data))
	an, ae := own.(io.WriterTo).WriteTo(anthropicOwnedBodyShortWriter{4})
	bn, be := legacy.(io.WriterTo).WriteTo(anthropicOwnedBodyShortWriter{4})
	if an != bn || !errors.Is(ae, io.ErrShortWrite) || !errors.Is(be, io.ErrShortWrite) {
		t.Fatalf("WriteTo parity %d/%v %d/%v", an, ae, bn, be)
	}
	got, err := anthropicReplayRequestBytes(own)
	want, werr := io.ReadAll(legacy)
	if !bytes.Equal(got, want) || err != werr {
		t.Fatalf("remaining=%q want=%q", got, want)
	}
}

type anthropicOwnedBodyForeignReader struct {
	reader        io.Reader
	reads, closes int
	err           error
}

func (r *anthropicOwnedBodyForeignReader) Read(p []byte) (int, error) {
	r.reads++
	if r.err != nil {
		return 0, r.err
	}
	return r.reader.Read(p)
}
func (r *anthropicOwnedBodyForeignReader) Close() error {
	r.closes++
	return errors.New("synthetic close error")
}

func TestAnthropicOwnedBodyReplayForeignGuard(t *testing.T) {
	sentinel := errors.New("synthetic read error")
	for _, readErr := range []error{nil, sentinel} {
		foreign := &anthropicOwnedBodyForeignReader{reader: strings.NewReader("custom"), err: readErr}
		got, err := anthropicReplayRequestBytes(foreign)
		if readErr == nil && string(got) != "custom" {
			t.Fatalf("foreign=%q", got)
		}
		if err != readErr || foreign.closes != 0 || foreign.reads == 0 {
			t.Fatalf("foreign read/close semantics: %v %d %d", err, foreign.reads, foreign.closes)
		}
		_ = foreign.Close()
		if foreign.closes != 1 {
			t.Fatal("Close callback lost")
		}
	}
	// A wrapper around the owned reader must keep wrapper read/close behavior.
	own, _ := anthropicOwnedGetBody([]byte("wrapped"))()
	wrapped := &anthropicOwnedBodyForeignReader{reader: own}
	got, err := anthropicReplayRequestBytes(wrapped)
	if err != nil || string(got) != "wrapped" || wrapped.reads == 0 {
		t.Fatal("wrapper bypassed")
	}
}

func TestAnthropicOwnedBodyCompleteActualGetBodyAndPrefix(t *testing.T) {
	for _, mode := range []string{"ordinary", "partial-owned", "foreign-body", "foreign-read-error", "factory-error", "wrong-request", "wrong-owned", "wrapped-owned", "no-getbody", "no-request"} {
		t.Run(mode, func(t *testing.T) {
			msgs := []Message{{Role: RoleSystem, Content: "synthetic system"}, {Role: RoleUser, Content: "fixture input"}}
			tools := []ToolDef{{Name: "fixture_tool", Description: "synthetic", Schema: `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`}}
			var sent, selected []byte
			var foreign *anthropicOwnedBodyForeignReader
			var replay io.ReadCloser
			var calls int
			p := newAnthropicOwnedBodyFixture(t, "https://fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
				var err error
				sent, err = io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				selected = sent
				original := req.GetBody
				resp := anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE)
				switch mode {
				case "partial-owned":
					req.GetBody = func() (io.ReadCloser, error) {
						calls++
						r, e := original()
						if e != nil {
							return r, e
						}
						_, _ = r.Read(make([]byte, 3))
						replay = r
						return r, nil
					}
					selected = sent[3:]
				case "foreign-body":
					selected = []byte(`{"system":"different","messages":[{"role":"user","content":"foreign"}]}`)
					foreign = &anthropicOwnedBodyForeignReader{reader: bytes.NewReader(selected)}
					req.GetBody = func() (io.ReadCloser, error) { calls++; return foreign, nil }
				case "foreign-read-error":
					foreign = &anthropicOwnedBodyForeignReader{reader: strings.NewReader("unused"), err: errors.New("synthetic read failure")}
					req.GetBody = func() (io.ReadCloser, error) { calls++; return foreign, nil }
				case "factory-error":
					req.GetBody = func() (io.ReadCloser, error) { calls++; return nil, errors.New("synthetic factory failure") }
				case "wrong-request":
					selected = []byte(`{"system":"wrong-request","messages":[]}`)
					foreign = &anthropicOwnedBodyForeignReader{reader: bytes.NewReader(selected)}
					resp.Request = &http.Request{GetBody: func() (io.ReadCloser, error) { calls++; return foreign, nil }}
				case "wrong-owned":
					selected = []byte(`{"system":"wrong-owned","messages":[]}`)
					resp.Request = &http.Request{GetBody: func() (io.ReadCloser, error) { calls++; return anthropicOwnedGetBody(selected)() }}
				case "wrapped-owned":
					req.GetBody = func() (io.ReadCloser, error) {
						calls++
						r, e := original()
						if e != nil {
							return r, e
						}
						foreign = &anthropicOwnedBodyForeignReader{reader: r}
						return foreign, nil
					}
				case "no-getbody":
					req.GetBody = nil
				case "no-request":
					resp.Request = nil
				default:
					req.GetBody = func() (io.ReadCloser, error) { calls++; return original() }
				}
				return resp, nil
			})
			want, err := buildAnthropicRequestWithSampling(p.cfg.Model, msgs, tools, true, p.cfg.MaxTokens, p.sampling)
			if err != nil {
				t.Fatal(err)
			}
			native := collectAnthropicOwnedBodyFixture(t, p, context.Background(), msgs, tools)
			if !bytes.Equal(sent, want) {
				t.Fatal("public wire differs from original builder")
			}
			if native.Prefix != anthropicRequestPrefix(selected) {
				t.Fatalf("prefix=%q want=%q", native.Prefix, anthropicRequestPrefix(selected))
			}
			if !strings.Contains(string(native.Data), "fixture-signature") || !strings.Contains(string(native.Data), `"extra":"preserve"`) {
				t.Fatalf("native raw lost: %s", native.Data)
			}
			if mode != "no-request" && mode != "no-getbody" && calls != 1 {
				t.Fatalf("GetBody calls=%d", calls)
			}
			if foreign != nil && (foreign.closes != 1 || foreign.reads == 0) {
				t.Fatalf("foreign closes=%d reads=%d", foreign.closes, foreign.reads)
			}
			if replay != nil {
				if n, e := replay.Read(make([]byte, 1)); n != 0 || e != io.EOF {
					t.Fatalf("partial replay not consumed: %d %v", n, e)
				}
			}
			sum := sha256.Sum256(sent)
			t.Logf("wire %dB sha256=%s native_prefix=%s", len(sent), hex.EncodeToString(sum[:]), native.Prefix)
		})
	}
}

func TestAnthropicOwnedBodyCompleteNilReplayPreservesError(t *testing.T) {
	p := newAnthropicOwnedBodyFixture(t, "https://nil.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
		req.GetBody = func() (io.ReadCloser, error) { return nil, nil }
		return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
	})
	out, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "nil replay fixture"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for d := range out {
		if d.Err != nil {
			streamErr = d.Err
		}
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "provider panic:") {
		t.Fatalf("legacy nil-reader error changed: %v", streamErr)
	}
}

func TestAnthropicOwnedBodyCompleteSignedHistoryPreserved(t *testing.T) {
	user := Message{Role: RoleUser, Content: "first synthetic turn"}
	tools := []ToolDef{{Name: "fixture", Schema: `{"type":"object"}`}}
	var sent [][]byte
	p := newAnthropicOwnedBodyFixture(t, "https://history.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		sent = append(sent, raw)
		return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
	})
	native := collectAnthropicOwnedBodyFixture(t, p, context.Background(), []Message{user}, tools)
	assistant := Message{Role: RoleAssistant, Content: "Done", Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: native}, {Type: PartTypeText, Text: "Done"}}}
	history := []Message{user, assistant, {Role: RoleUser, Content: "continue"}}
	original, _ := json.Marshal(history)
	collectAnthropicOwnedBodyFixture(t, p, context.Background(), history, tools)
	after, _ := json.Marshal(history)
	if !bytes.Equal(original, after) {
		t.Fatal("signed history mutated")
	}
	if len(sent) != 2 || !bytes.Contains(sent[1], []byte(`"signature":"fixture-signature"`)) || !bytes.Contains(sent[1], []byte(`"extra":"preserve"`)) {
		t.Fatalf("signed history not replayed: %s", sent[1])
	}
	expected, err := buildAnthropicRequestWithSampling(p.cfg.Model, history, tools, true, p.cfg.MaxTokens, p.sampling)
	if err != nil || !bytes.Equal(sent[1], expected) {
		t.Fatalf("replayed wire differs: %v", err)
	}
}

func TestAnthropicOwnedBodyCompleteImageFallbackAndRedirect(t *testing.T) {
	t.Run("image fallback", func(t *testing.T) {
		msgs := []Message{{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "synthetic image task"}, {Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}}}}}
		var sent [][]byte
		p := newAnthropicOwnedBodyFixture(t, "https://images.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
			raw, e := io.ReadAll(req.Body)
			if e != nil {
				return nil, e
			}
			sent = append(sent, raw)
			if len(sent) == 1 {
				return anthropicOwnedBodyResponse(req, 400, `{"error":"image input is not supported"}`), nil
			}
			return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
		})
		primary, e := buildAnthropicRequestWithSampling(p.cfg.Model, msgs, nil, true, p.cfg.MaxTokens, p.sampling)
		if e != nil {
			t.Fatal(e)
		}
		fallback, e := buildAnthropicRequestWithSampling(p.cfg.Model, msgs, nil, false, p.cfg.MaxTokens, p.sampling)
		if e != nil {
			t.Fatal(e)
		}
		native := collectAnthropicOwnedBodyFixture(t, p, context.Background(), msgs, nil)
		if len(sent) != 2 || !bytes.Equal(sent[0], primary) || !bytes.Equal(sent[1], fallback) || native.Prefix != anthropicRequestPrefix(fallback) {
			t.Fatal("negotiated fallback bytes/prefix differ")
		}
		collectAnthropicOwnedBodyFixture(t, p, context.Background(), msgs, nil)
		if len(sent) != 3 || !bytes.Equal(sent[2], fallback) {
			t.Fatal("learned image rejection route differs")
		}
	})
	t.Run("429 zero wait replay", func(t *testing.T) {
		var sent [][]byte
		p := newAnthropicOwnedBodyFixture(t, "https://retry.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
			raw, e := io.ReadAll(req.Body)
			if e != nil {
				return nil, e
			}
			sent = append(sent, raw)
			if len(sent) == 1 {
				resp := anthropicOwnedBodyResponse(req, 429, `{"error":"rate limit"}`)
				resp.Header.Set("Retry-After", "0")
				return resp, nil
			}
			return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
		})
		native := collectAnthropicOwnedBodyFixture(t, p, context.Background(), []Message{{Role: RoleUser, Content: "retry fixture"}}, nil)
		if len(sent) != 2 || !bytes.Equal(sent[0], sent[1]) || native.Prefix != anthropicRequestPrefix(sent[1]) {
			t.Fatal("bounded retry wire/prefix differs")
		}
	})
	t.Run("307 replay", func(t *testing.T) {
		var sent [][]byte
		p := newAnthropicOwnedBodyFixture(t, "https://redirect.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
			raw, e := io.ReadAll(req.Body)
			if e != nil {
				return nil, e
			}
			sent = append(sent, raw)
			if len(sent) == 1 {
				resp := anthropicOwnedBodyResponse(req, 307, "")
				resp.Header.Set("Location", "https://accepted.fixture.invalid/messages")
				return resp, nil
			}
			return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
		})
		native := collectAnthropicOwnedBodyFixture(t, p, context.Background(), []Message{{Role: RoleUser, Content: "redirect fixture"}}, nil)
		if len(sent) != 2 || !bytes.Equal(sent[0], sent[1]) || native.Prefix != anthropicRequestPrefix(sent[1]) {
			t.Fatal("307 replay/prefix differs")
		}
	})
}

func TestAnthropicOwnedBodyCompleteZenUnchanged(t *testing.T) {
	var sent []byte
	p := newAnthropicOwnedBodyFixture(t, "https://opencode.ai/zen/v1", func(req *http.Request) (*http.Response, error) {
		r, e := req.GetBody()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		if _, changed := r.(*anthropicOwnedRequestBody); changed {
			t.Fatal("special Zen GetBody changed")
		}
		sent, e = io.ReadAll(req.Body)
		if e != nil {
			return nil, e
		}
		if req.Header.Get("x-api-key") != "public" || req.Header.Get("x-opencode-session") != normalizeZenSessionID("fixture-session") {
			t.Fatalf("Zen headers=%v", req.Header)
		}
		return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
	})
	msgs := []Message{{Role: RoleUser, Content: "synthetic Zen"}}
	native := collectAnthropicOwnedBodyFixture(t, p, WithOpenCodeSession(context.Background(), "fixture-session"), msgs, nil)
	want, e := buildAnthropicRequestWithSampling(p.cfg.Model, msgs, nil, true, p.cfg.MaxTokens, p.sampling)
	if e != nil || !bytes.Equal(sent, want) || native.Prefix != anthropicRequestPrefix(want) {
		t.Fatalf("Zen wire/prefix changed: %v", e)
	}
}

func TestAnthropicOwnedBodyCompleteCancelledBeforeTransport(t *testing.T) {
	p := newAnthropicOwnedBodyFixture(t, "https://cancel.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
		t.Fatal("transport reached on cancelled context")
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "synthetic"}}, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v %v", out, err)
	}
}

func BenchmarkAnthropicOwnedRequestComplete(b *testing.B) {
	for _, size := range []int{16, 64 << 10, 1 << 20} {
		name := "tiny"
		if size == 64<<10 {
			name = "64KiB"
		}
		if size == 1<<20 {
			name = "1MiB"
		}
		b.Run(name, func(b *testing.B) {
			p := newAnthropicOwnedBodyFixture(b, "https://bench.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
				_, e := io.Copy(io.Discard, req.Body)
				if e != nil {
					return nil, e
				}
				return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
			})
			msgs := []Message{{Role: RoleUser, Content: strings.Repeat("s", size)}}
			tools := []ToolDef{{Name: "fixture", Description: "synthetic", Schema: `{"type":"object"}`}}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				collectAnthropicOwnedBodyFixture(b, p, context.Background(), msgs, tools)
			}
		})
	}
}
