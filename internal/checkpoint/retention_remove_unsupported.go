//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package checkpoint

type retentionNativeIdentity struct{}

func retentionFileIdentity(string) (retentionNativeIdentity, error) {
	return retentionNativeIdentity{}, ErrStoreInventory
}

func retentionRemoveProven(string, *retentionFileProof) error {
	return ErrStoreInventory
}
