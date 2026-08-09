package evalmini

import (
	"reflect"
	"testing"
	"time"
)

func TestAdd(t *testing.T) { if got := Add(20, 22); got != 42 { t.Fatalf("got %d", got) } }

func TestClamp(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-2, 0}, {5, 5}, {20, 10}} {
		if got := Clamp(tc.in, 0, 10); got != tc.want { t.Fatalf("Clamp(%d)=%d want %d", tc.in, got, tc.want) }
	}
}

func TestNormalizeSpace(t *testing.T) { if got := NormalizeSpace("  alpha\t beta\n gamma "); got != "alpha beta gamma" { t.Fatalf("%q", got) } }

func TestDeduplicate(t *testing.T) {
	want := []string{"a", "b", "c"}; if got := Deduplicate([]string{"a", "b", "a", "c", "b"}); !reflect.DeepEqual(got, want) { t.Fatalf("%v", got) }
}

func TestParsePort(t *testing.T) {
	if got, err := ParsePort("8080"); err != nil || got != 8080 { t.Fatalf("got %d err %v", got, err) }
	for _, bad := range []string{"", "0", "65536", "abc"} { if _, err := ParsePort(bad); err == nil { t.Fatalf("%q accepted", bad) } }
}

func TestSlug(t *testing.T) { if got := Slug("  Hello, SUPER Cli!  "); got != "hello-super-cli" { t.Fatalf("%q", got) } }

func TestRetryDelay(t *testing.T) {
	want := []time.Duration{time.Second, 2*time.Second, 4*time.Second, 8*time.Second, 8*time.Second}
	for i, d := range want { if got := RetryDelay(i); got != d { t.Fatalf("attempt %d: %v", i, got) } }
}

func TestRedact(t *testing.T) {
	if got := Redact("Authorization: Bearer secret-123; secret-123", "secret-123"); got != "Authorization: Bearer [REDACTED]; [REDACTED]" { t.Fatalf("%q", got) }
	if got := Redact("safe", ""); got != "safe" { t.Fatalf("empty secret: %q", got) }
}

func TestChunk(t *testing.T) {
	want := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if got := Chunk([]string{"a", "b", "c", "d", "e"}, 2); !reflect.DeepEqual(got, want) { t.Fatalf("%v", got) }
	if got := Chunk([]string{"a"}, 0); got != nil { t.Fatalf("invalid size = %v", got) }
}

func TestIsLocalURL(t *testing.T) {
	for _, raw := range []string{"http://localhost:8080/v1", "http://127.0.0.1", "http://192.168.1.20:1234"} { if !IsLocalURL(raw) { t.Fatalf("local rejected: %s", raw) } }
	for _, raw := range []string{"https://api.openai.com/v1", "not a url"} { if IsLocalURL(raw) { t.Fatalf("remote/invalid accepted: %s", raw) } }
}
