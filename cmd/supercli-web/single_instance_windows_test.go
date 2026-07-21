//go:build windows

package main

import (
	"fmt"
	"testing"
	"time"
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
