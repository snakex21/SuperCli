//go:build windows

package checkpoint

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStoreRetentionRestoresReadOnlyOnNativeDeleteFailure(t *testing.T) {
	m, _, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		path := retentionObjectPath(m, retentionOrphanGitObjects(t, m)[0])
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		proof, err := retentionProveFile(path, info)
		if err != nil {
			t.Fatal(err)
		}
		name, err := retentionWindowsPath(path)
		if err != nil {
			t.Fatal(err)
		}
		original, err := windows.GetFileAttributes(name)
		if err != nil || original&windows.FILE_ATTRIBUTE_READONLY == 0 {
			t.Fatalf("native Git fixture has no READONLY: %v", err)
		}
		failure := errors.New("synthetic disposition denied")
		err = retentionRemoveWindows(path, proof, func(handle windows.Handle) error {
			var current windows.ByHandleFileInformation
			if err := windows.GetFileInformationByHandle(handle, &current); err != nil {
				t.Fatal(err)
			}
			if current.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
				t.Fatal("readonly was not cleared before native disposition")
			}
			return failure
		})
		current, attrErr := windows.GetFileAttributes(name)
		if !errors.Is(err, failure) || attrErr != nil || current != original {
			t.Fatalf("failed-delete attrs were not restored: original=%x current=%x err=%v attrs=%v", original, current, err, attrErr)
		}
		if err := retentionCheckProof(path, proof); err != nil {
			t.Fatal("failed disposition changed file identity or bytes:", err)
		}
	})
}
