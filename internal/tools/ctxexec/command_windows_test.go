//go:build windows

package ctxexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdScriptsKeepWindowsQuoting(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "read me.txt"), []byte("quoted path works"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"builtin", []string{"cmd", "/c", "dir /b"}, "read me.txt"},
		{"quoted file", []string{"cmd", "/c", "type \"read me.txt\""}, "quoted path works"},
		{"split arguments", []string{"cmd", "/c", "type", "read me.txt"}, "quoted path works"},
		{"conditional", []string{"cmd", "/c", "if exist \"read me.txt\" (type \"read me.txt\") else (echo missing)"}, "quoted path works"},
		{"literal quotes", []string{"cmd", "/c", "echo \"hello\""}, "\"hello\""},
		{"nested shell", []string{"cmd", "/c", "cmd /c dir /b"}, "read me.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := New(home).Run(context.Background(), &Request{Command: tc.args})
			if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, tc.want) {
				t.Fatalf("err=%v result=%+v", err, res)
			}
		})
	}
}
