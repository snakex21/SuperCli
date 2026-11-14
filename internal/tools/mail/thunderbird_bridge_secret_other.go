//go:build !windows

package mail

import (
	"errors"
	"os"
	"strings"
)

// Without DPAPI the secret relies on file permissions (0600 file, 0700 dir),
// like an SSH private key.
func thunderbirdProtectSecret(secret []byte) (string, error) {
	return "raw:" + b64.EncodeToString(secret), nil
}

func thunderbirdUnprotectSecret(value string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(value, "raw:")
	if !ok {
		return nil, errors.New("pairing secret was protected on another platform")
	}
	return b64.DecodeString(encoded)
}

func thunderbirdRestrictPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}
