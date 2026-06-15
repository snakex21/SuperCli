package memory

import (
	"os"
	"testing"
)

// readFile is a test helper that returns the contents of path.
// The *testing.T is unused but kept in the signature so future
// helpers can call t.Helper() if needed.
func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
