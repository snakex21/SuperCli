package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestFactoryCodexNamedAccountsUseResolvedOptionsLabelsAndRoundRobin(t *testing.T) {
	dir := t.TempDir()
	for _, label := range []string{"first-fixture", "second-fixture"} {
		if err := codexauth.Save(codexauth.AuthFilePathFor(dir, label), &codexauth.AuthFile{Tokens: &codexauth.TokenData{AccessToken: "old-fixture", RefreshToken: "refresh-fixture", AccountID: label}}); err != nil {
			t.Fatal(err)
		}
	}
	refreshes := 0
	var accounts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshes++
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["client_id"] != "fixture-client" {
				t.Error("custom OAuth client id was lost")
			}
			fmt.Fprint(w, `{"access_token":"fresh-fixture"}`)
		case "/backend/responses":
			if r.Header.Get("Authorization") != "Bearer fresh-fixture" {
				t.Error("refresh did not use configured issuer")
			}
			accounts = append(accounts, r.Header.Get("ChatGPT-Account-Id"))
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	f := New(nil, dir, llm.NewCapabilityRegistry())
	f.SetCodexAuthOptions(codexauth.Options{BackendURL: srv.URL + "/backend", Issuer: srv.URL, ClientID: "fixture-client"})
	p, err := f.Build(config.Config{Provider: config.ProviderCodex, Model: "future-fixture"}, llm.PurposeMain)
	if err != nil {
		t.Fatal(err)
	}
	router, ok := llm.Unwrap(p).(*llm.RouterProvider)
	if !ok || router.LabelAt(0) != "first-fixture" || router.LabelAt(1) != "second-fixture" || router.ModelName() != "future-fixture" {
		t.Fatal("named account pool lost labels or raw model id")
	}
	for i := 0; i < 2; i++ {
		ch, err := p.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "fixture"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for delta := range ch {
			if delta.Err != nil {
				t.Fatal(delta.Err)
			}
		}
	}
	if refreshes != 2 || len(accounts) != 2 || accounts[0] != "first-fixture" || accounts[1] != "second-fixture" {
		t.Fatalf("wrong round robin/options: refreshes=%d accounts=%v", refreshes, accounts)
	}
}

func TestDefaultFactoryPreservesAuthoritativeNegativeCapabilities(t *testing.T) {
	dir := t.TempDir()
	if err := codexauth.Save(codexauth.AuthFilePathFor(dir, "named-fixture"), &codexauth.AuthFile{Tokens: &codexauth.TokenData{AccessToken: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "gpt-5-fixture", VisionKnown: true, ReasoningKnown: true, Source: llm.SourceProvider})
	if _, err := Default(config.Config{Provider: config.ProviderCodex, Model: "gpt-5-fixture"}, dir, caps); err != nil {
		t.Fatal(err)
	}
	info, _ := caps.Get("gpt-5-fixture")
	if info.Vision || info.Reasoning {
		t.Fatalf("heuristic overwrote server metadata: %+v", info)
	}
}
