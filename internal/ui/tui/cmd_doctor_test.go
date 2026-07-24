package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/system/doctor"
)

func TestRenderDoctorReport(t *testing.T) {
	rep := doctor.Report{Version: "test", Checks: []doctor.Check{{Name: "home", Status: doctor.OK, Detail: "/tmp"}, {Name: "provider", Status: doctor.Warn, Detail: "echo", Remediation: "configure provider"}}}
	out := renderDoctorReport(rep, NoColorPalette(), 80)
	for _, want := range []string{"Doctor", "home", "provider", "configure provider"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestDoctorModalCloses(t *testing.T) {
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir()})
	rep := doctor.Report{Version: "test", Checks: []doctor.Check{{Name: "home", Status: doctor.OK, Detail: "/tmp"}}}
	m.mode = modeDoctor
	m.doctorReport = &rep
	out, _ := m.handleDoctorKey(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.mode != modeNormal || mm.doctorReport != nil {
		t.Fatalf("doctor modal should close, mode=%v report=%v", mm.mode, mm.doctorReport)
	}
}

func TestRenderDoctorReportTruncatesLongBinaryPath(t *testing.T) {
	rep := doctor.Report{Version: "test", Checks: []doctor.Check{{Name: "binary", Status: doctor.OK, Detail: strings.Repeat("C:/very/long/path/", 12) + "supercli.exe"}}}
	out := renderDoctorReport(rep, NoColorPalette(), 60)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "│") && len([]rune(line)) > 62 {
			t.Fatalf("line too wide (%d): %q\n%s", len([]rune(line)), line, out)
		}
	}
}
