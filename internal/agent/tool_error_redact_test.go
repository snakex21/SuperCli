package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactToolErrorArgsWriteFileContentDropped(t *testing.T) {
	secret := "package main // super private file body"
	raw, _ := json.Marshal(map[string]any{"path": "main.go", "content": secret})
	got := redactToolErrorArgs(raw)
	if strings.Contains(got, "private file body") {
		t.Fatalf("content leaked: %s", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Fatalf("path should be kept: %s", got)
	}
	if !strings.Contains(got, "<redacted 39 bytes sha256:") {
		t.Fatalf("missing content placeholder: %s", got)
	}
}

func TestRedactToolErrorArgsPatchEditsDropped(t *testing.T) {
	raw := []byte(`{"path":"a.go","edits":[{"old":"OLDSECRET","new":"NEWSECRET"}],"patch":"--- a\n+++ b\n+PATCHBODY"}`)
	got := redactToolErrorArgs(raw)
	for _, s := range []string{"OLDSECRET", "NEWSECRET", "PATCHBODY"} {
		if strings.Contains(got, s) {
			t.Fatalf("%s leaked: %s", s, got)
		}
	}
}

func TestRedactToolErrorArgsMailBodyDropped(t *testing.T) {
	raw := []byte(`{"to":"bob@example.com","subject":"hi","body":"Dear Bob, my PIN is 1234","html":"<p>secret html</p>"}`)
	got := redactToolErrorArgs(raw)
	if strings.Contains(got, "PIN is 1234") || strings.Contains(got, "secret html") {
		t.Fatalf("mail body leaked: %s", got)
	}
}

func TestRedactToolErrorArgsSecretKeysMasked(t *testing.T) {
	raw := []byte(`{"api_key":"sk-AAA","password":"hunter2","Authorization":"Bearer zzz","headers":{"X-Api-Key":"kkk","Cookie":"sid=1"},"auth_token":"ttt"}`)
	got := redactToolErrorArgs(raw)
	for _, s := range []string{"sk-AAA", "hunter2", "zzz", "kkk", "sid=1", "ttt"} {
		if strings.Contains(got, s) {
			t.Fatalf("secret %q leaked: %s", s, got)
		}
	}
	if !strings.Contains(got, "<masked>") {
		t.Fatalf("expected mask marker: %s", got)
	}
}

func TestRedactToolErrorArgsURLSecretsMasked(t *testing.T) {
	raw := []byte(`{"url":"https://user:pw123@api.example.com/v1/x?page=2&access_token=TOK123&apiKey=K9#id_token=FRAG"}`)
	got := redactToolErrorArgs(raw)
	for _, s := range []string{"TOK123", "K9", "pw123", "FRAG"} {
		if strings.Contains(got, s) {
			t.Fatalf("url secret %q leaked: %s", s, got)
		}
	}
	if !strings.Contains(got, "page=2") || !strings.Contains(got, "api.example.com") {
		t.Fatalf("non-secret url parts should stay: %s", got)
	}
}

func TestRedactToolErrorArgsInlineCommandSecret(t *testing.T) {
	raw := []byte(`{"command":"curl -H 'Authorization: Bearer abc.def' --password=hunter2 https://x.io"}`)
	got := redactToolErrorArgs(raw)
	if strings.Contains(got, "abc.def") || strings.Contains(got, "hunter2") {
		t.Fatalf("inline secret leaked: %s", got)
	}
}

func TestRedactToolErrorArgsTruncation(t *testing.T) {
	long := strings.Repeat("x", 5000)
	raw, _ := json.Marshal(map[string]any{"path": long})
	got := redactToolErrorArgs(raw)
	if strings.Contains(got, strings.Repeat("x", toolErrorArgsMaxStrRune+1)) {
		t.Fatalf("long string not truncated (len %d)", len(got))
	}
	if !strings.Contains(got, "truncated 5000 bytes") {
		t.Fatalf("missing truncation marker: %s", got)
	}

	many := map[string]any{}
	for i := 0; i < 100; i++ {
		many[strings.Repeat("k", 10)+string(rune('a'+i%26))+strings.Repeat("z", i)] = strings.Repeat("v", 100)
	}
	raw, _ = json.Marshal(many)
	got = redactToolErrorArgs(raw)
	if len(got) > toolErrorArgsMaxBytes+64 {
		t.Fatalf("whole args not capped: %d bytes", len(got))
	}
	if !strings.Contains(got, "...<truncated") {
		t.Fatalf("missing overall truncation marker: %s", got)
	}
}

func TestRedactToolErrorArgsInvalidJSON(t *testing.T) {
	raw := []byte(`{"content": "secret stuff", broken`)
	got := redactToolErrorArgs(raw)
	if got != "<unparseable 34 bytes>" {
		t.Fatalf("got %q", got)
	}
	if redactToolErrorArgs(nil) != "" {
		t.Fatalf("empty args should stay empty")
	}
}
