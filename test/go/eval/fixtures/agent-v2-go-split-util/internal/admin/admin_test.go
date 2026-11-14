package admin

import (
	"strings"
	"testing"
	"time"
)

func TestDescribe(t *testing.T) {
	now := time.Now()
	e := Record("login", now.Add(-10*time.Minute))
	if got := Describe(e, now, 80); !strings.Contains(got, "login (10m ago)") {
		t.Fatal(got)
	}
}
