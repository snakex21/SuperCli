package providers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestVerifyZenUsesExistingCatalogTransportAndGate(t *testing.T) {
	for _, tc := range []struct{ transport, endpoint string }{
		{llm.ModelTransportOpenAICompatible, "/zen/v1/chat/completions"},
		{llm.ModelTransportResponses, "/zen/v1/responses"},
	} {
		t.Run(tc.transport, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != tc.endpoint || r.Method != http.MethodPost {
					t.Errorf("request = %s %s; want POST %s", r.Method, r.URL.Path, tc.endpoint)
				}
				if r.Header.Get("Authorization") != "Bearer public" {
					t.Errorf("public key missing: %q", r.Header.Get("Authorization"))
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if tc.transport == llm.ModelTransportResponses {
					// The existing Responses dialect omits empty tools and uses
					// the same session key in the body and Zen headers.
					if _, ok := body["tools"]; ok {
						t.Error("empty Responses tools must be omitted")
					}
					if key, _ := body["prompt_cache_key"].(string); key == "" || key != r.Header.Get("x-opencode-session") {
						t.Errorf("Responses cache/session key mismatch: %q", key)
					}
					if body["max_output_tokens"] != float64(32000) {
						t.Error("existing Responses token limit not preserved")
					}
				} else {
					tools, _ := json.Marshal(body["tools"])
					for _, name := range []string{"bash", "read"} {
						if !strings.Contains(string(tools), "\"name\":\""+name+"\"") {
							t.Errorf("mandatory Zen tool %q missing", name)
						}
					}
				}
				if body["model"] != "fixture-free" {
					t.Error("incorrect selected model")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.transport == llm.ModelTransportResponses {
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
				} else {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()

			// Keep the real Zen URL for its existing routing/payload checks, but
			// send every request to this TLS fixture. No cloud request is made.
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				if err != nil {
					return nil, err
				}
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				client := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: server.Certificate().DNSNames[0]})
				if err := client.HandshakeContext(ctx); err != nil {
					_ = conn.Close()
					return nil, err
				}
				return client, nil
			}
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() {
				http.DefaultTransport = previous
				transport.CloseIdleConnections()
			}()

			dir := t.TempDir()
			catalog := map[string]any{
				"fetched_at": time.Now(),
				"models": map[string]llm.ModelInfo{
					"fixture-free": {ID: "fixture-free", Transport: tc.transport, ReasoningKnown: true, Source: llm.SourceExternal},
				},
			}
			data, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "opencode_zen_catalog.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := VerifyConnectionForProvider(context.Background(), config.ProviderOpenAI,
				"https://opencode.ai/zen/v1", "", "fixture-free", dir); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls = %d; want one verification request using the cached catalog", calls)
			}
		})
	}
}
