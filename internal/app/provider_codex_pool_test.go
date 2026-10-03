package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestBuildCodexPoolUsesOnlyNamedAccountInsteadOfMissingDefault(t *testing.T) {
	dir := t.TempDir()
	if err := codexauth.Save(codexauth.AuthFilePathFor(dir, "only-fixture"), &codexauth.AuthFile{LastRefresh: time.Now(), Tokens: &codexauth.TokenData{AccessToken: "named-fixture-token", AccountID: "named-fixture-account"}}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer named-fixture-token" || r.Header.Get("ChatGPT-Account-Id") != "named-fixture-account" {
			t.Error("wrong named account transport")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer srv.Close()
	old := codexAuthMgr
	codexAuthMgr = codexauth.NewManager(dir, codexauth.Options{BackendURL: srv.URL, Issuer: "https://issuer-fixture.invalid", ClientID: "fixture-client"})
	t.Cleanup(func() { codexAuthMgr = old })
	p, err := buildCodexPool(config.Config{Model: "future-fixture"}, dir, llm.NewCapabilityRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*llm.CodexProvider); !ok {
		t.Fatal("one named account should remain a plain provider")
	}
	ch, err := p.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "fixture"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
}
