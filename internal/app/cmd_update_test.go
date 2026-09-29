package app

import (
	"context"
	"errors"
	"supercli/internal/system/updater"
	"testing"
)

type fakeCLIUpdater struct {
	state updater.State
	err   error
	calls []string
}

func (f *fakeCLIUpdater) Check(context.Context) (updater.State, error) {
	f.calls = append(f.calls, "check")
	return f.state, f.err
}
func (f *fakeCLIUpdater) Download(context.Context) (updater.State, error) {
	f.calls = append(f.calls, "download")
	return f.state, f.err
}
func (f *fakeCLIUpdater) Install(context.Context) (updater.State, error) {
	f.calls = append(f.calls, "install")
	return updater.State{Status: "installed", RestartRequired: true}, f.err
}
func TestCLIUpdateInstallsOnlyVerifiedPendingDownload(t *testing.T) {
	for _, tc := range []struct {
		status     string
		wantCalls  int
		wantStatus string
	}{
		{"current", 1, "current"}, {"installed", 1, "installed"}, {"downloaded", 2, "installed"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f := &fakeCLIUpdater{state: updater.State{Status: tc.status}}
			state, err := performCLIUpdate(context.Background(), f, "install")
			if err != nil || len(f.calls) != tc.wantCalls || state.Status != tc.wantStatus {
				t.Fatalf("state=%+v calls=%v err=%v", state, f.calls, err)
			}
		})
	}
	f := &fakeCLIUpdater{err: errors.New("checksum mismatch")}
	if _, err := performCLIUpdate(context.Background(), f, "install"); err == nil || len(f.calls) != 1 {
		t.Fatal("failed download reached installation")
	}
	f = &fakeCLIUpdater{state: updater.State{Status: "available"}}
	if _, err := performCLIUpdate(context.Background(), f, "check"); err != nil || len(f.calls) != 1 || f.calls[0] != "check" {
		t.Fatal("checking attempted a download")
	}
}
