package server

import (
	"testing"

	"tiler/config"
)

func TestValidate(t *testing.T) {
	s := New(config.Config{MaxZoom: 5, Workers: 3})
	if _, err := s.Validate(6, 0, 0); err == nil {
		t.Fatal("want error")
	}
	if s.QueueSize() != 6 {
		t.Fatal(s.QueueSize())
	}
}
