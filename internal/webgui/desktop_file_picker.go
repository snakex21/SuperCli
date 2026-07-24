package webgui

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	ps := `$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Application]::EnableVisualStyles(); $owner = New-Object System.Windows.Forms.Form; $owner.StartPosition = 'CenterScreen'; $owner.ShowInTaskbar = $false; $owner.TopMost = $true; $owner.Width = 1; $owner.Height = 1; $owner.Opacity = 0; $owner.Show(); $owner.Activate(); $d = New-Object System.Windows.Forms.OpenFileDialog; $d.Title = 'Wybierz pliki'; $d.Multiselect = $true; $d.CheckFileExists = $true; $d.Filter = 'Obsługiwane pliki|*.png;*.jpg;*.jpeg;*.webp;*.gif;*.pdf;*.docx;*.xlsx;*.csv;*.zip;*.txt;*.md;*.json;*.yaml;*.yml;*.xml;*.html;*.css;*.js;*.ts;*.tsx;*.go;*.py|Obrazy|*.png;*.jpg;*.jpeg;*.webp;*.gif|Dokumenty i arkusze|*.pdf;*.docx;*.xlsx;*.csv|Tekst, kod i archiwa|*.zip;*.txt;*.md;*.json;*.yaml;*.yml;*.xml;*.html;*.css;*.js;*.ts;*.tsx;*.go;*.py|Wszystkie pliki|*.*'; if (Test-Path -LiteralPath $env:SUPERCLI_PICKER_HOME -PathType Container) { $d.InitialDirectory = $env:SUPERCLI_PICKER_HOME }; $r = $d.ShowDialog($owner); $paths = @(); if ($r -eq [System.Windows.Forms.DialogResult]::OK) { $paths = @($d.FileNames) }; $json = ConvertTo-Json -InputObject $paths -Compress; [Console]::Write($json); $owner.Close(); $owner.Dispose()`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", ps)
	cmd.Env = append(os.Environ(), "SUPERCLI_PICKER_HOME="+s.filePickerHome())
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
	paths, err = stagePickedAttachments(s.eng.Home(), paths)
	if err != nil {
		http.Error(w, "could not prepare attachments: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"paths": paths, "workspace": s.eng.Home()})
}

func (s *Server) filePickerHome() string {
	if !strings.EqualFold(s.runtimeAppName(), "NestCafe") {
		return s.eng.Home()
	}
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return s.eng.Home()
	}
	for _, name := range []string{"Documents", "Desktop"} {
		candidate := filepath.Join(userHome, name)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
	}
	return userHome
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
