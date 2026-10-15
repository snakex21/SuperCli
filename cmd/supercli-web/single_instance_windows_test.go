//go:build windows

package main

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestSingleInstanceIsScopedByApplicationProfile(t *testing.T) {
	base := fmt.Sprintf("test-%d", time.Now().UnixNano())
	releaseSuper, already, err := claimSingleInstance(base + "-supercli")
	if err != nil || already {
		t.Fatalf("first SuperCli claim: already=%v err=%v", already, err)
	}
	defer releaseSuper()
	releaseDuplicate, already, err := claimSingleInstance(base + "-supercli")
	if err != nil || !already {
		t.Fatalf("duplicate SuperCli claim: already=%v err=%v", already, err)
	}
	releaseDuplicate()

	releaseNestCafe, already, err := claimSingleInstance(base + "-nestcafe")
	if err != nil || already {
		t.Fatalf("NestCafe profile collided with SuperCli: already=%v err=%v", already, err)
	}
	releaseNestCafe()
}

func TestShowExistingInstanceSignalsListener(t *testing.T) {
	profile := fmt.Sprintf("test-show-%d", time.Now().UnixNano())
	if showExistingInstance(profile) {
		t.Fatal("show request succeeded without a running instance")
	}
	name, err := windows.UTF16PtrFromString(showEventName(profile))
	if err != nil {
		t.Fatal(err)
	}
	event, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	if !showExistingInstance(profile) {
		t.Fatal("show request failed with a listening instance")
	}
	if result, err := windows.WaitForSingleObject(event, 1000); err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("event not signalled: result=%d err=%v", result, err)
	}
}

func TestSingleInstanceMutexIsScopedByExecutablePath(t *testing.T) {
	first := singleInstanceMutexName("supercli", `C:\Apps\First\SuperCli.exe`)
	same := singleInstanceMutexName("supercli", `c:\apps\first\SuperCli.exe`)
	second := singleInstanceMutexName("supercli", `C:\Apps\Second\SuperCli.exe`)
	nestCafe := singleInstanceMutexName("nestcafe", `C:\Apps\First\SuperCli.exe`)
	if first != same {
		t.Fatalf("path casing changed identity: %q != %q", first, same)
	}
	if first == second {
		t.Fatalf("separate executable copies share mutex %q", first)
	}
	if first == nestCafe {
		t.Fatalf("separate application profiles share mutex %q", first)
	}
}
