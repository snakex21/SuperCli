package webgui

import "testing"

func TestTaskbarBadgeLabel(t *testing.T) {
	for count, want := range map[int]string{-1: "", 0: "", 1: "1", 9: "9", 10: "9+", 42: "9+"} {
		if got := taskbarBadgeLabel(count); got != want {
			t.Fatalf("taskbarBadgeLabel(%d) = %q, want %q", count, got, want)
		}
	}
}

func TestTaskbarBadgePixelsAreBoundedAndVisible(t *testing.T) {
	pixels := taskbarBadgePixels(2)
	if len(pixels) != 16*16*4 {
		t.Fatalf("pixel buffer length = %d", len(pixels))
	}
	visible, white := 0, 0
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i+3] != 0 {
			visible++
		}
		if pixels[i] == 255 && pixels[i+1] == 255 && pixels[i+2] == 255 && pixels[i+3] == 255 {
			white++
		}
	}
	if visible < 100 || visible >= 256 {
		t.Fatalf("unexpected visible area: %d pixels", visible)
	}
	if white == 0 {
		t.Fatal("badge number is not visible")
	}
}
