package proxy

import (
	"strings"
	"testing"
	"time"
)

func TestProxyHelpers(t *testing.T) {
	to, err := ParseTimeouts("5", "1m")
	if err != nil || to.Dial != 5*time.Second || to.Read != time.Minute {
		t.Fatalf("%+v %v", to, err)
	}
	if r := NewRequest("/x"); !strings.HasPrefix(r.ID, "req_") {
		t.Fatal(r.ID)
	}
	if ids := Batch([]string{"a", "b"}); len(ids) != 2 || ids[0] == ids[1] {
		t.Fatal(ids)
	}
}
