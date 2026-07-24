//go:build windows

package webgui

import (
	"testing"

	"golang.org/x/sys/windows"
)

func assertTokenFileRestricted(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("read token DACL: %v", err)
	}
	dacl, defaulted, err := sd.DACL()
	if err != nil {
		t.Fatalf("read token ACL: %v", err)
	}
	if defaulted || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("token DACL defaulted=%v acl=%v; want one explicit current-user ACE", defaulted, dacl)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("read token security control: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("token DACL inherits permissions; want protected DACL")
	}
}
