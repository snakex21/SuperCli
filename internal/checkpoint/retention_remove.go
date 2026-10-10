package checkpoint

import (
	"os"
)

// Native identity is captured during the census, before a path can be replaced.
// A hard link is never a candidate: clearing Windows READONLY would also change
// every other name of that file, and unlinking it would not reclaim its bytes.
type retentionFileProof struct {
	info   os.FileInfo
	native retentionNativeIdentity
}

func retentionProveFile(path string, info os.FileInfo) (*retentionFileProof, error) {
	if info == nil || !info.Mode().IsRegular() || !os.SameFile(info, info) {
		return nil, ErrStoreInventory
	}
	native, err := retentionFileIdentity(path)
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return nil, ErrStoreInventory
	}
	return &retentionFileProof{info: info, native: native}, nil
}

func retentionCheckProof(path string, proof *retentionFileProof) error {
	if proof == nil {
		return ErrStoreInventory
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(proof.info, info) || info.Size() != proof.info.Size() || !info.ModTime().Equal(proof.info.ModTime()) {
		return ErrStoreInventory
	}
	native, err := retentionFileIdentity(path)
	if err != nil || native != proof.native {
		return ErrStoreInventory
	}
	return nil
}
