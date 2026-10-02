package viewport

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	upstream "github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The pinned implementation is the oracle: cached widths must not change bytes,
// scroll positions, public slice behavior, or messages sent to the renderer.
func assertUpstreamParity(t *testing.T, got Model, want upstream.Model) {
	t.Helper()
	if got.View() != want.View() {
		t.Fatalf("view differs at y=%d width=%d height=%d", got.YOffset, got.Width, got.Height)
	}
	gotState := []any{got.YOffset, got.AtTop(), got.AtBottom(), got.PastBottom(), got.ScrollPercent(), got.HorizontalScrollPercent(), got.TotalLineCount(), got.VisibleLineCount()}
	wantState := []any{want.YOffset, want.AtTop(), want.AtBottom(), want.PastBottom(), want.ScrollPercent(), want.HorizontalScrollPercent(), want.TotalLineCount(), want.VisibleLineCount()}
	if !reflect.DeepEqual(gotState, wantState) {
		t.Fatalf("state differs: got %v, want %v", gotState, wantState)
	}
}

func assertLinesParity(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("returned lines differ: got %q, want %q", got, want)
	}
}

func assertCommandParity(t *testing.T, got, want tea.Cmd) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatal("renderer command presence differs")
	}
	if got != nil && !reflect.DeepEqual(got(), want()) {
		t.Fatal("renderer command message differs")
	}
}

func TestViewportSetContentMatchesPinnedUpstream(t *testing.T) {
	content := []string{"", "short", "short\n", "short\n\n", "short\nsecond\nthird", "short\nsecond\nthird\nfourth", "short\nchanged and wider\nthird\nfourth", "short\r\nchanged and wider\r\nthird\r\n", "wide 界 e\u0301 👩‍💻\n\x1b[31mcolored text\x1b[0m\n\tcontrol\rline", "z\nx", "", strings.Repeat("x", 65534), strings.Repeat("x", 65535), strings.Repeat("x", 65536), strings.Repeat("\x1b[31m\x1b[0m", 10000) + "tiny", "end\n"}
	styles := []lipgloss.Style{lipgloss.NewStyle(), lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1, 2), lipgloss.NewStyle().Width(8).Height(2)}
	for _, size := range [][2]int{{0, 0}, {1, 1}, {10, 2}, {37, 5}} {
		for si, style := range styles {
			// The upstream renderer requires its frame to fit the viewport.
			if style.GetHorizontalFrameSize() > size[0] || style.GetVerticalFrameSize() > size[1] {
				continue
			}
			t.Run(fmt.Sprintf("%dx%d/style%d", size[0], size[1], si), func(t *testing.T) {
				got, want := New(size[0], size[1]), upstream.New(size[0], size[1])
				got.Style, want.Style = style, style
				for _, s := range content {
					got.SetContent(s)
					want.SetContent(s)
					assertUpstreamParity(t, got, want)
					for _, n := range []int{-1, 0, 1, 5, 70000} {
						got.SetXOffset(n)
						want.SetXOffset(n)
						assertUpstreamParity(t, got, want)
						got.SetYOffset(n)
						want.SetYOffset(n)
						assertUpstreamParity(t, got, want)
					}
					assertLinesParity(t, got.GotoTop(), want.GotoTop())
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.ScrollDown(1), want.ScrollDown(1))
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.PageDown(), want.PageDown())
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.HalfPageDown(), want.HalfPageDown())
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.GotoBottom(), want.GotoBottom())
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.ScrollUp(1), want.ScrollUp(1))
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.PageUp(), want.PageUp())
					assertUpstreamParity(t, got, want)
					assertLinesParity(t, got.HalfPageUp(), want.HalfPageUp())
					assertUpstreamParity(t, got, want)
					got.ScrollRight(3)
					want.ScrollRight(3)
					assertUpstreamParity(t, got, want)
					got.ScrollLeft(2)
					want.ScrollLeft(2)
					assertUpstreamParity(t, got, want)
				}
			})
		}
	}
}

func TestViewportWidthReuseSurvivesPublicLineAlias(t *testing.T) {
	got, want := New(10, 2), upstream.New(10, 2)
	got.SetContent("short\nsecond\nthird")
	want.SetContent("short\nsecond\nthird")
	a, b := got.GotoBottom(), want.GotoBottom()
	a[0], b[0] = "abcdefghijklmnopqrstu", "abcdefghijklmnopqrstu"
	for _, s := range []string{"short\nabcdefghijklmnopqrstu\nthird", "short\nsecond\nthird"} {
		got.SetContent(s)
		want.SetContent(s)
		got.SetXOffset(5)
		want.SetXOffset(5)
		assertUpstreamParity(t, got, want)
	}
}

func TestViewportCopiedModelsOwnWidthMetadata(t *testing.T) {
	original, oracle := New(10, 2), upstream.New(10, 2)
	original.SetContent("one\ntwo\nthree")
	oracle.SetContent("one\ntwo\nthree")
	copied, copiedOracle := original, oracle
	oldWidths := append([]uint16(nil), copied.lineWidths...)
	original.SetContent("a much wider line\nchanged\nthree\nfour")
	if !reflect.DeepEqual(copied.lineWidths, oldWidths) {
		t.Fatal("SetContent mutated copied width metadata")
	}
	copied.SetContent("one\ntwo\nthree\nnew tail")
	copiedOracle.SetContent("one\ntwo\nthree\nnew tail")
	assertUpstreamParity(t, copied, copiedOracle)
	original.SetContent("")
	if original.widthContent != "" || len(original.lineWidths) != 1 || len(original.lines) != 1 {
		t.Fatal("reset retained old content metadata")
	}
	if copied.widthContent != "one\ntwo\nthree\nnew tail" {
		t.Fatal("reset affected copied model")
	}
}

func TestViewportOversizeWidthUsesExactFallback(t *testing.T) {
	m := New(10, 2)
	for _, width := range []int{65534, 65535, 65536} {
		s := strings.Repeat("x", width)
		m.SetContent(s)
		m.SetContent(s)
		if m.longestLineWidth != width {
			t.Fatalf("width %d clamped to %d", width, m.longestLineWidth)
		}
		cached := uint16(width)
		if width >= 65535 {
			cached = 65535
		}
		if m.lineWidths[0] != cached {
			t.Fatalf("incorrect sentinel for width %d", width)
		}
	}
	colored := strings.Repeat("\x1b[31m\x1b[0m", 10000) + "tiny"
	m.SetContent(colored)
	m.SetContent(colored)
	if m.longestLineWidth != ansi.StringWidth(colored) || m.lineWidths[0] != 4 {
		t.Fatal("ANSI byte length was treated as terminal width")
	}
}

func TestViewportKeyboardMouseAndRendererCommandsMatchUpstream(t *testing.T) {
	content := strings.Repeat("abcdefghijklmnop 界\n", 25)
	events := []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyPgDown}, tea.KeyMsg{Type: tea.KeyUp}, tea.KeyMsg{Type: tea.KeyPgUp}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")}, tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyLeft}, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, Shift: true}, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelLeft}, tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonWheelDown}}
	for _, high := range []bool{false, true} {
		got, want := New(10, 3), upstream.New(10, 3)
		got.HighPerformanceRendering, want.HighPerformanceRendering = high, high
		got.YPosition, want.YPosition = 2, 2
		got.SetHorizontalStep(3)
		want.SetHorizontalStep(3)
		got.SetContent(content)
		want.SetContent(content)
		assertCommandParity(t, Sync(got), upstream.Sync(want))
		for _, event := range events {
			var a, b tea.Cmd
			got, a = got.Update(event)
			want, b = want.Update(event)
			assertCommandParity(t, a, b)
			assertUpstreamParity(t, got, want)
			got.SetContent(content + "new tail")
			want.SetContent(content + "new tail")
		}
	}
	var got Model
	var want upstream.Model
	var a, b tea.Cmd
	got, a = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	want, b = want.Update(tea.KeyMsg{Type: tea.KeyDown})
	assertCommandParity(t, a, b)
	assertUpstreamParity(t, got, want)
}
