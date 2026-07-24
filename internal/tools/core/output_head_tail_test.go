package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHeadTail_Fits(t *testing.T) {
	if got := HeadTail("hello", 10, 10); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadTail_Caps(t *testing.T) {
	s := "AAAA" + strings.Repeat("x", 100) + "ZZZZ"
	got := HeadTail(s, 4, 4)
	if !strings.HasPrefix(got, "AAAA\n[... omitted_bytes=100 ...]\n") {
		t.Fatalf("head/marker wrong: %q", got)
	}
	if !strings.HasSuffix(got, "ZZZZ") {
		t.Fatalf("tail wrong: %q", got)
	}
}

func TestHeadTail_UTF8Boundaries(t *testing.T) {
	// "ż" is 2 bytes; head cut at 5 lands mid-rune and must move back.
	s := strings.Repeat("ż", 20) // 40 bytes
	got := HeadTail(s, 5, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
}

func TestHeadTailBuffer_NoTruncation(t *testing.T) {
	b := NewHeadTailBuffer(8, 8)
	b.Write([]byte("abc"))
	b.Write([]byte("def"))
	if b.Truncated() {
		t.Fatal("unexpected truncation")
	}
	if got := b.String(); got != "abcdef" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadTailBuffer_DropsMiddle(t *testing.T) {
	b := NewHeadTailBuffer(4, 4)
	b.Write([]byte("HEAD"))
	for i := 0; i < 100; i++ {
		b.Write([]byte("xx"))
	}
	b.Write([]byte("TAIL"))
	if !b.Truncated() {
		t.Fatal("expected truncation")
	}
	got := b.String()
	if !strings.HasPrefix(got, "HEAD") {
		t.Fatalf("head lost: %q", got)
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Fatalf("tail lost: %q", got)
	}
	if !strings.Contains(got, "omitted_bytes=200") {
		t.Fatalf("omitted count wrong: %q", got)
	}
}

func TestHeadTailBuffer_HugeSingleWrite(t *testing.T) {
	b := NewHeadTailBuffer(4, 4)
	b.Write([]byte("HD" + strings.Repeat("x", 1000) + "TL"))
	got := b.String()
	if !strings.HasPrefix(got, "HDxx") || !strings.HasSuffix(got, "xxTL") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "omitted_bytes=996") {
		t.Fatalf("omitted count wrong: %q", got)
	}
}

func TestHeadTailBuffer_UTF8SafeCuts(t *testing.T) {
	b := NewHeadTailBuffer(5, 5)
	b.Write([]byte(strings.Repeat("ż", 50))) // 100 bytes, cuts land mid-rune
	got := b.String()
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "omitted_bytes=") {
		t.Fatalf("marker missing: %q", got)
	}
}
