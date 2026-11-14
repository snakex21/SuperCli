package auth

import (
	"testing"
	"time"
)

func TestSession(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := Start("Jane Doe", now.Add(-3*time.Hour))
	if s.User != "jane-doe" || Age(s, now) != "3h ago" {
		t.Fatalf("%+v %s", s, Age(s, now))
	}
	if MaxAge("") != 7*24*time.Hour || MaxAge("90") != 90*time.Second {
		t.Fatal("MaxAge")
	}
}
