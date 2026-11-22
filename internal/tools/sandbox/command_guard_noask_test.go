package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The command from a reported approval prompt: a PowerShell build with
// variables, Join-Path, Start-Process, a pipe and Set-Location. Ordinary work
// like this must never stop on a question.
const reportedZigBuild = `$ErrorActionPreference='Stop'; $p=(Get-Location).Path; $d=Join-Path $p ('build-migration\survival-check-'+(Get-Date -Format 'yyyyMMdd-HHmmss-fff')); New-Item -ItemType Directory -Path $d,(Join-Path $d 'temp') | Out-Null; $env:TEMP=Join-Path $d 'temp'; $env:TMP=$env:TEMP; Write-Output $d; Set-Location (Join-Path $p 'zig-engine'); $exe=Join-Path $p 'build-migration\tools\zig-x86_64-windows-0.15.2\zig.exe'; $proc=Start-Process -FilePath ('"'+$exe+'"') -ArgumentList @('build','client','test','--cache-dir',('"'+(Join-Path $d 'local-cache')+'"'),'--global-cache-dir',('"'+(Join-Path $d 'global-cache')+'"'),'--prefix',('"'+(Join-Path $d 'native')+'"'),'--summary','all') -RedirectStandardOutput (Join-Path $d 'build.stdout.log') -RedirectStandardError (Join-Path $d 'build.stderr.log') -NoNewWindow -Wait -PassThru; Get-Content -LiteralPath (Join-Path $d 'build.stdout.log'),(Join-Path $d 'build.stderr.log') -Tail 100; exit $proc.ExitCode`

func TestCheckCommandNeverAsksOrdinaryWork(t *testing.T) {
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	ordinary := []string{
		reportedZigBuild,
		`$out = Join-Path $PWD 'zig-out'; if (Test-Path $out) { Remove-Item -Recurse -Force $out }; & $env:ZIG build -Doptimize=ReleaseSafe`,
		`Set-Location src; Get-ChildItem -Recurse *.tmp | Remove-Item -Force; go build ./...`,
		`$sw=[Diagnostics.Stopwatch]::StartNew(); & .\build.ps1 -Config Release | Tee-Object build.log; "took $($sw.Elapsed)"`,
		`Get-Process | Where-Object { $_.Name -like 'zig*' } | ForEach-Object { $_.Id }`,
		`Start-Process -FilePath $exe -ArgumentList 'build' -Wait -NoNewWindow; Remove-Item -Recurse -Force $HOME\.cache\zig`,
		`git reset --hard HEAD; git clean -fdx`,
	}
	for _, cmd := range ordinary {
		for _, argv := range [][]string{{"powershell", "-NoProfile", "-Command", cmd}, {"pwsh", "-c", cmd}} {
			if got := CheckCommand(argv, project, ""); got.Verdict != CommandAllow {
				t.Errorf("ordinary command stopped: %q -> %+v", argv, got)
			}
		}
		if got := CheckShellText(cmd+"\n", project, ""); got.Verdict != CommandAllow {
			t.Errorf("typed ordinary command stopped: %q -> %+v", cmd, got)
		}
	}
	for _, argv := range [][]string{
		{"powershell", "-File", `scripts\build.ps1`},
		{"powershell", "-EncodedCommand", "SQBFAFgA"},
		{"bash", "-c", `for f in *.zig; do zig fmt "$f"; done && rm -rf "$BUILD_DIR"`},
		{"cmd", "/c", `set OUT=%CD%\out && rd /s /q %OUT%`},
		{"sudo", "make", "install"},
		{"git", "push", "--force-with-lease"},
	} {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandAllow {
			t.Errorf("ordinary command stopped: %q -> %+v", argv, got)
		}
	}
}

func TestCheckCommandBlocksLiteralCatastrophes(t *testing.T) {
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	catastrophic := [][]string{
		{"rm", "-rf", "/"},
		{"rm", "-rf", "~"},
		{"bash", "-c", "rm -rf ~/"},
		{"format", "C:"},
		{"git", "push", "--force", "origin", "main"},
		{"git", "push", "-f"},
	}
	if runtime.GOOS == "windows" {
		home, _ := os.UserHomeDir()
		catastrophic = append(catastrophic,
			[]string{"pwsh", "-c", `Remove-Item -Recurse -Force C:\`},
			[]string{"pwsh", "-c", `Remove-Item -Recurse -Force C:\Windows`},
			[]string{"pwsh", "-c", `Remove-Item -Recurse -Force C:\Users`},
			[]string{"pwsh", "-c", `Remove-Item -Recurse -Force "C:\Program Files"`},
			[]string{"cmd", "/c", `rd /s /q "C:\Program Files"`},
			[]string{"pwsh", "-c", "Remove-Item -Recurse -Force " + home},
		)
	}
	for _, argv := range catastrophic {
		got := CheckCommand(argv, project, "")
		if got.Verdict != CommandBlock {
			t.Errorf("catastrophic command not blocked: %q -> %+v", argv, got)
		}
		if strings.TrimSpace(got.Reason) == "" {
			t.Errorf("block without a reason: %q", argv)
		}
	}
}
