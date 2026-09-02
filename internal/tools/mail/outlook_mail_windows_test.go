//go:build windows

package mail

import (
	"testing"
	"time"
)

func TestSaneMSGTime(t *testing.T) {
	if got := saneMSGTime(time.Date(2051, 3, 1, 3, 31, 12, 0, time.Local)); !got.IsZero() {
		t.Fatalf("future sentinel date must be rejected, got %v", got)
	}
	valid := time.Now().Add(-24 * time.Hour)
	if got := saneMSGTime(valid); got.IsZero() {
		t.Fatal("recent valid message date was rejected")
	}
}

func TestOutlookDefaultFolderAlias(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"Inbox", olFolderInbox},
		{"Skrzynka odbiorcza", olFolderInbox},
		{"Kosz", olFolderDeletedItems},
		{"Trash", olFolderDeletedItems},
		{"Wysłane", olFolderSentMail},
		{"Wersje robocze", olFolderDrafts},
		{"Spam", olFolderJunk},
		{"Błędy synchronizacji", olFolderSyncIssues},
	}
	for _, tt := range tests {
		got, ok := outlookDefaultFolderAlias(tt.name)
		if !ok || got != tt.want {
			t.Fatalf("alias %q = (%d,%v), want (%d,true)", tt.name, got, ok, tt.want)
		}
	}
	if _, ok := outlookDefaultFolderAlias("[Gmail]/Kosz"); ok {
		t.Fatal("path must not be mistaken for a single-folder alias")
	}
}
