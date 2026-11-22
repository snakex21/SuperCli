package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckCommandMatrix(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "me-other-folder")
	block := [][]string{
		{"rm", "-rf", "/"},
		{"rm", "-rf", "~"},
		{"rm", "-rf", "*"},
		{"rm", "-r", "-f", ".."},
		{"rm", "-rf", "src/../.."},
		{"rm", "-rf", ".git"},
		{"rm", "-rf", "/home/user"},
		{"rm", "-rf", other},
		{"cmd", "/c", `rd /s /q C:\`},
		{"cmd", "/c", `del /s /q C:\*`},
		{"powershell", "-NoProfile", "-Command", `Remove-Item -Recurse -Force C:\`},
		{"powershell", "-Command", `Remove-Item -r C:\Users\me`},
		{"powershell", "-Command", `Remove-Item -Rec -Path C:\Users\me`},
		{"pwsh", "-c", `ri -Recurse C:\Users\me`},
		{"pwsh", "-c", `Get-ChildItem; rm -r -fo C:\Windows`},
		{"bash", "-c", "cd /tmp && rm -rf /"},
		{"bash", "-lc", "find / -delete"},
		{"find", ".", "-name", "*.o", "-exec", "rm", "{}", ";", "-delete"},
		{"format", "D:"},
		{"diskpart"},
		{"shutdown", "/s", "/t", "0"},
		{"dd", "if=/dev/zero", "of=/dev/sda"},
		{"git", "push", "--force", "origin", "main"},
		{"git", "push", "origin", "+main"},
		{"git", "reset", "--hard", "HEAD~3"},
		{"git", "-C", "x", "reset", "--hard"},
		{"git", "-c", "core.x=y", "clean", "-fdx"},
		{"sudo", "rm", "-rf", "/var/lib"},
		{"env", "A=1", "rm", "-rf", "/"},
	}
	ask := [][]string{
		{"rm", "-rf", "${HOME}"},
		{"rm", "-rf", "$DIR"},
		{"bash", "-c", "rm -rf \"$BUILD_DIR\""},
		{"cmd", "/c", "rd /s /q %TEMPDIR%"},
		{"powershell", "-Command", "Remove-Item -Recurse $env:OUT"},
		{"powershell", "-EncodedCommand", "SQBFAFgA"},
		{"bash", "-c", "ls | xargs rm -rf"},
	}
	allow := [][]string{
		{"go", "test", "./..."},
		{"zig", "build", "client"},
		{"rm", "-rf", "build"},
		{"rm", "-rf", "./zig-cache"},
		{"rm", "-f", "*.o"},
		{"rm", "-rf", "build/*"},
		{"cmd", "/c", "rd /s /q zig-out"},
		{"git", "status"},
		{"git", "commit", "-m", "fix bug & reboot fixture"},
		{"git", "commit", "-m", "format code; shutdown hook | rm -rf /"},
		{"bash", "-c", `git commit -m "fix bug & reboot fixture"`},
		{"powershell", "-Command", `git commit -m 'a; Remove-Item -Recurse C:\'`},
		{"git", "push", "origin", "main"},
		{"git", "reset", "--soft", "HEAD~1"},
		{"git", "clean", "-n"},
		{"powershell", "-Command", `Remove-Item build\out.txt`},
		{"echo", "rm -rf / is dangerous"},
		{"find", ".", "-name", "*.go"},
	}
	for _, argv := range block {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandBlock {
			t.Errorf("not blocked: %q -> %+v", argv, got)
		}
	}
	for _, argv := range ask {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandAsk {
			t.Errorf("not asked: %q -> %+v", argv, got)
		}
	}
	for _, argv := range allow {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandAllow {
			t.Errorf("ordinary command refused: %q -> %+v", argv, got)
		}
	}
	if runtime.GOOS == "windows" {
		if got := CheckCommand([]string{"cmd", "/c", `rd /s /q "C:\Program Files"`}, project, ""); got.Verdict != CommandBlock {
			t.Errorf("system folder delete not blocked: %+v", got)
		}
	}
}

func TestCheckShellTextForInteractiveSessions(t *testing.T) {
	project := t.TempDir()
	if got := CheckShellText("cd src && rm -rf /\n", project, ""); got.Verdict != CommandBlock {
		t.Fatalf("typed rm -rf / not blocked: %+v", got)
	}
	if got := CheckShellText("go test ./...\n", project, ""); got.Verdict != CommandAllow {
		t.Fatalf("typed test refused: %+v", got)
	}
}

func TestSplitShellKeepsQuotedSeparators(t *testing.T) {
	segs := splitShell(`git commit -m "a & b; c | d" && echo 'x;y'`, shellPOSIX)
	if len(segs) != 2 || strings.Join(segs[0], "|") != "git|commit|-m|a & b; c | d" || strings.Join(segs[1], "|") != "echo|x;y" {
		t.Fatalf("segments = %q", segs)
	}
	segs = splitShell("Remove-Item -Path 'C:\\a b' -Recurse; Write-Host `\"x`\"", shellPwsh)
	if len(segs) != 2 || segs[0][2] != `C:\a b` {
		t.Fatalf("pwsh segments = %q", segs)
	}
}
