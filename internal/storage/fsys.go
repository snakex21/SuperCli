package storage

import "os"

// mkdirAll is split out so tests can override filesystem behaviour
// without sprinkling t.Skip across the suite. The real implementation
// just delegates to os.MkdirAll.
var mkdirAll = func(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}
