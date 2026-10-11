package llm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type probeDeadlineTransport func(*http.Request) (*http.Response, error)

func (f probeDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpencodeProbeBoundsProvidedGenerationClient(t *testing.T) {
	for _, shorter := range []bool{false, true} {
		t.Run(map[bool]string{false: "bounded discovery", true: "shorter caller deadline"}[shorter], func(t *testing.T) {
			provider, err := NewOpencode(OpencodeConfig{BaseURL: "https://models.example.invalid/v1", Model: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			var expected time.Time
			if shorter {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Minute/10)
				defer cancel()
				expected, _ = ctx.Deadline()
			}
			calls := 0
			client := &http.Client{Transport: probeDeadlineTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := req.Context().Deadline()
				if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) <= 0 {
					t.Fatalf("metadata request has no bounded deadline: %v/%v", deadline, ok)
				}
				if shorter && !deadline.Equal(expected) {
					t.Fatalf("caller deadline changed: got=%v want=%v", deadline, expected)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"fixture"}]}`))}, nil
			})}
			provider.inner.http = client
			models, err := provider.ProbeModels(ctx)
			if err != nil || calls != 1 || len(models) != 1 || models[0].ID != "opencode/fixture" {
				t.Fatalf("discovery changed: models=%+v err=%v calls=%d", models, err, calls)
			}
			if client.Timeout != 0 || provider.inner.http != client {
				t.Fatal("discovery changed the generation client's timeout or identity")
			}
		})
	}
}
