//go:build !windows && !linux && !darwin

package session

import "fmt"

func renameMediaDelete(string, string) error {
	// A check-then-os.Rename fallback can replace an unexpected empty original
	// directory. Refuse instead of weakening the no-overwrite recovery contract.
	return fmt.Errorf("%w: exclusive media rename is unsupported on this operating system", ErrMediaDeleteRecovery)
}
