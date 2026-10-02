package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func markerTestStyle(label string) lipgloss.Style {
	return lipgloss.NewStyle().Transform(func(s string) string { return label + ":" + s })
}

func TestMarkerPalettePreservesValueSemantics(t *testing.T) {
	p := Palette{
		Marker:     markerTestStyle("Marker"),
		MarkerDim:  markerTestStyle("MarkerDim"),
		Dim:        markerTestStyle("Dim"),
		Error:      markerTestStyle("Error"),
		Success:    markerTestStyle("Success"),
		ToolName:   markerTestStyle("ToolName"),
		ToolOutput: markerTestStyle("ToolOutput"),
		ToolErr:    markerTestStyle("ToolErr"),
	}
	m := NewMarker(p)
	copied := m
	styles := []struct {
		name           string
		source, marker *lipgloss.Style
		copied         *lipgloss.Style
	}{
		{"Marker", &p.Marker, &m.p.Marker, &copied.p.Marker},
		{"MarkerDim", &p.MarkerDim, &m.p.MarkerDim, &copied.p.MarkerDim},
		{"Dim", &p.Dim, &m.p.Dim, &copied.p.Dim},
		{"Error", &p.Error, &m.p.Error, &copied.p.Error},
		{"Success", &p.Success, &m.p.Success, &copied.p.Success},
		{"ToolName", &p.ToolName, &m.p.ToolName, &copied.p.ToolName},
		{"ToolOutput", &p.ToolOutput, &m.p.ToolOutput, &copied.p.ToolOutput},
		{"ToolErr", &p.ToolErr, &m.p.ToolErr, &copied.p.ToolErr},
	}
	for _, style := range styles {
		t.Run(style.name, func(t *testing.T) {
			want := style.name + ":sample"
			if got := style.marker.Render("sample"); got != want {
				t.Fatalf("constructor lost style: got %q, want %q", got, want)
			}
			*style.source = markerTestStyle("changed source")
			if got := style.marker.Render("sample"); got != want {
				t.Fatalf("source palette mutation changed marker: %q", got)
			}
			if got := style.copied.Render("sample"); got != want {
				t.Fatalf("copy lost style: got %q, want %q", got, want)
			}
			*style.copied = markerTestStyle("changed copy")
			if got := style.marker.Render("sample"); got != want {
				t.Fatalf("copied marker mutation changed original: %q", got)
			}
		})
	}
}

func TestMarkerZeroValueAndNoColorStyles(t *testing.T) {
	var zero Marker
	plain := NewMarker(Palette{})
	noColor := NewMarker(NoColorPalette())
	markers := []struct {
		name   string
		render func(Marker) string
	}{
		{"marker", func(m Marker) string { return m.Goal(1, 2) }},
		{"marker dim", func(m Marker) string { return m.CouncilAllFailed() }},
		{"dim", func(m Marker) string { return m.Done(1, 2) }},
		{"error", func(m Marker) string { return m.Error(errors.New("failed")) }},
		{"success", func(m Marker) string { return m.ToolResultFull("read_lines", "line", false) }},
		{"tool name", func(m Marker) string { return m.ToolCall("read_lines", "{}") }},
		{"tool output", func(m Marker) string { return m.ToolResult("line", false) }},
		{"tool error", func(m Marker) string { return m.ToolResult("failed", true) }},
	}
	for _, marker := range markers {
		t.Run(marker.name, func(t *testing.T) {
			want := marker.render(plain)
			if got := marker.render(zero); got != want {
				t.Fatalf("zero marker changed: got %q, want %q", got, want)
			}
			if got := marker.render(noColor); got != want || strings.Contains(got, "\x1b") {
				t.Fatalf("no-color marker changed: got %q, want %q", got, want)
			}
		})
	}
}
