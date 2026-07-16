package webgui

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"supercli/internal/system/childproc"
)

// handleFilePicker opens the operating system's file selector. It returns
// paths only; chat submission validates them again through the live sandbox.
func (s *Server) handleFilePicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if runtime.GOOS != "windows" {
		http.Error(w, "native file picker is currently available on Windows", http.StatusNotImplemented)
		return
	}
	ps := `$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Application]::EnableVisualStyles(); $owner = New-Object System.Windows.Forms.Form; $owner.StartPosition = 'CenterScreen'; $owner.ShowInTaskbar = $false; $owner.TopMost = $true; $owner.Width = 1; $owner.Height = 1; $owner.Opacity = 0; $owner.Show(); $owner.Activate(); $d = New-Object System.Windows.Forms.OpenFileDialog; $d.Title = 'Wybierz pliki dla NestCafe'; $d.Multiselect = $true; $d.CheckFileExists = $true; $d.Filter = 'Dokumenty biurowe|*.docx;*.xlsx;*.pdf;*.txt;*.md;*.csv|Dokumenty Word|*.docx|Arkusze Excel|*.xlsx;*.csv|PDF|*.pdf|Wszystkie pliki|*.*'; if (Test-Path -LiteralPath $env:SUPERCLI_PICKER_HOME -PathType Container) { $d.InitialDirectory = $env:SUPERCLI_PICKER_HOME }; $r = $d.ShowDialog($owner); $paths = @(); if ($r -eq [System.Windows.Forms.DialogResult]::OK) { $paths = @($d.FileNames) }; $json = ConvertTo-Json -InputObject $paths -Compress; [Console]::Write($json); $owner.Close(); $owner.Dispose()`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", ps)
	cmd.Env = append(os.Environ(), "SUPERCLI_PICKER_HOME="+s.eng.Home())
	childproc.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, "dialog failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
		return
	}
	paths, err := parsePickerPaths(out)
	if err != nil {
		http.Error(w, "dialog returned invalid data", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"paths": paths, "workspace": s.eng.Home()})
}

func parsePickerPaths(raw []byte) ([]string, error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\xEF\xBB\xBF"))
	if text == "" {
		return []string{}, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(text), &paths); err != nil {
		return nil, err
	}
	if paths == nil {
		paths = []string{}
	}
	return paths, nil
}
