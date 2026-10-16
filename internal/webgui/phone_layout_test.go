package webgui

import (
	"strings"
	"testing"
)

func TestPhoneLayoutAndSyncAssets(t *testing.T) {
	raw, err := assetsFS.ReadFile("assets/phone.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"@media (max-width:560px)", "overflow-x:hidden", "grid-template-columns:44px minmax(0,1fr) auto", "#run-status", "text-overflow:ellipsis", "white-space:nowrap", "env(safe-area-inset-bottom)", "env(safe-area-inset-left)", ".question-actions { flex-wrap:wrap; }", ".toast { width:max-content"} {
		if !strings.Contains(string(raw), rule) {
			t.Errorf("missing phone rule: %s", rule)
		}
	}
	html, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `src="js/05b-sync.js"`) {
		t.Fatal("sync module not loaded")
	}
	js, err := assetsFS.ReadFile("assets/js/05b-sync.js")
	if err != nil || !strings.Contains(string(js), "/api/sync/ws") {
		t.Fatal("sync module not embedded", err)
	}
}
