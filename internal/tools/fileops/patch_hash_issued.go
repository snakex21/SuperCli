package fileops

import (
	"path/filepath"
	"strings"
	"sync"
)

// Hashes that patch_file has itself shown to the model, per file. base_hash
// is honoured only when it is one of these: models were observed inventing a
// plausible SHA-256 on their first edit, and rejecting that as "file changed"
// was both false and a wasted round. An invented hash is ignored with a note;
// an issued one still rejects an edit after a change by anyone else.
const (
	issuedHashFiles   = 4096
	issuedHashPerFile = 16
)

var issuedHashes = struct {
	sync.Mutex
	byPath map[string][]string
}{byPath: map[string][]string{}}

func issuedHashKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func noteIssuedHash(path string, hashes ...string) {
	key := issuedHashKey(path)
	issuedHashes.Lock()
	defer issuedHashes.Unlock()
	if _, ok := issuedHashes.byPath[key]; !ok && len(issuedHashes.byPath) >= issuedHashFiles {
		issuedHashes.byPath = map[string][]string{}
	}
	list := issuedHashes.byPath[key]
	for _, hash := range hashes {
		hash = strings.ToLower(hash)
		known := false
		for _, existing := range list {
			if existing == hash {
				known = true
				break
			}
		}
		if !known {
			list = append(list, hash)
		}
	}
	if len(list) > issuedHashPerFile {
		list = list[len(list)-issuedHashPerFile:]
	}
	issuedHashes.byPath[key] = list
}

func hashIssued(path, hash string) bool {
	hash = strings.ToLower(strings.TrimSpace(hash))
	issuedHashes.Lock()
	defer issuedHashes.Unlock()
	for _, existing := range issuedHashes.byPath[issuedHashKey(path)] {
		if existing == hash {
			return true
		}
	}
	return false
}
