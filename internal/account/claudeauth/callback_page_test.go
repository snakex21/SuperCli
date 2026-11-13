package claudeauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClaudeCallbackSuccessPageIsLocalizedAndReturnURLIsLocal(t *testing.T) {
	page := callbackPage(Options{Language: "pl", ReturnURL: "http://127.0.0.1:8765/?code=secret"}, true)
	if !strings.Contains(page, "Konto Claude połączone") || !strings.Contains(page, `href="http://127.0.0.1:8765/"`) || strings.Contains(page, "secret") {
		t.Fatal("missing safe localized success page")
	}
	for _, address := range []string{"https://attacker.example/", "javascript:alert(1)", "http://user@localhost/", "//localhost/"} {
		if callbackReturnURL(address) != "" {
			t.Fatalf("unsafe callback return target: %s", address)
		}
	}
}

func TestClaudeCallbackDoesNotClaimSuccessWhenTokenExchangeFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "token-secret", http.StatusUnauthorized) }))
	defer server.Close()
	m := NewManager(t.TempDir(), Options{TokenURL: server.URL, Language: "pl"})
	var html string
	err := m.Login(context.Background(), func(raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?code=code&state=" + url.QueryEscape(q.Get("state")))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		html = string(b)
		if resp.StatusCode == http.StatusOK {
			t.Error("failed exchange returned success")
		}
		return nil
	})
	if err == nil || m.LoggedIn() || !strings.Contains(html, "Nie udało się połączyć Claude") || strings.Contains(html, "token-secret") {
		t.Fatalf("incorrect failure page: %v", err)
	}
}
