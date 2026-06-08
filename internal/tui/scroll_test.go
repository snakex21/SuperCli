package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

// mockKeyMsg implements teaKeyMsg for testing.
type mockKeyMsg struct{ s string }

func (m mockKeyMsg) String() string { return m.s }

func TestHandleScroll_PgUp(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "pgup"}, ScrollConfig{})
	if !consumed {
		t.Fatal("pgup should be consumed")
	}
}

func TestHandleScroll_PgDown(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "pgdown"}, ScrollConfig{})
	if !consumed {
		t.Fatal("pgdown should be consumed")
	}
}

func TestHandleScroll_ArrowUp(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "up"}, ScrollConfig{})
	if !consumed {
		t.Fatal("up should be consumed")
	}
}

func TestHandleScroll_ArrowDown(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "down"}, ScrollConfig{})
	if !consumed {
		t.Fatal("down should be consumed")
	}
}

func TestHandleScroll_Home(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "home"}, ScrollConfig{})
	if !consumed {
		t.Fatal("home should be consumed")
	}
}

func TestHandleScroll_End(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "end"}, ScrollConfig{})
	if !consumed {
		t.Fatal("end should be consumed")
	}
}

func TestHandleScroll_UnknownKey(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("line1\nline2\nline3\nline4\nline5")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "ctrl+x"}, ScrollConfig{})
	if consumed {
		t.Fatal("unknown key should not be consumed")
	}
}

func TestHandleScroll_EmptyContent(t *testing.T) {
	vp := viewport.New(80, 20)
	vp.SetContent("")
	consumed := HandleScroll(&vp, mockKeyMsg{s: "pgup"}, ScrollConfig{})
	if !consumed {
		t.Fatal("scroll on empty content should still be consumed")
	}
}
