package memory

import "os"

// osStat is os.Stat under a memory-package alias so the test
// file can assert "not exists" semantics.
func osStat(path string) (os.FileInfo, error) { return os.Stat(path) }

func errIsNotExist(err error) bool { return os.IsNotExist(err) }
