package claudeauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

func TestClaudeCallbackPageNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCallbackPage(w, Options{Language: "pl", ReturnURL: "http://127.0.0.1:8765/"}, true, http.StatusOK)
	}))
	defer server.Close()
	for _, width := range []int{1000, 420} {
		browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: width, Height: 850})
		if err != nil {
			t.Fatal(err)
		}
		page, err := browser.NewPage(ctx, server.URL)
		if err != nil {
			browser.Close()
			t.Fatal(err)
		}
		var failure string
		err = page.Eval(ctx, `(()=>{if(document.querySelector('h1').textContent!=='Konto Claude połączone')return 'missing localized success';if(document.querySelector('a.primary').href!=='http://127.0.0.1:8765/')return 'return link broken';if(document.documentElement.scrollWidth>innerWidth)return 'horizontal overflow';return '';})()`, &failure)
		if err != nil || failure != "" {
			browser.Close()
			t.Fatalf("%v / %s", err, failure)
		}
		if out := os.Getenv("SUPERCLI_AUTH_PREVIEW_DIR"); out != "" {
			if err := os.MkdirAll(out, 0700); err != nil {
				t.Fatal(err)
			}
			png, err := page.Screenshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("claude-browser-%d.png", width)), png, 0600); err != nil {
				t.Fatal(err)
			}
		}
		browser.Close()
	}
}
