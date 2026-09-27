package core

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// CommandKey identifies a command and its explicit environment overrides.
// It is internal execution metadata, never a tool-output or prompt field.
func CommandKey(command []string, workdir string, env []string) [sha256.Size]byte {
	value := struct {
		Command []string
		Workdir string
		Env     []string
	}{command, filepath.Clean(workdir), canonicalCommandEnv(env, runtime.GOOS == "windows")}
	encoded, _ := json.Marshal(value)
	return sha256.Sum256(encoded)
}

func canonicalCommandEnv(env []string, windows bool) []string {
	if len(env) == 0 {
		return nil
	}
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if windows {
			key = strings.ToLower(key)
		}
		values[key] = value // Execution uses the last override for a repeated key.
	}
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		keys[i] = key + "=" + values[key]
	}
	return keys
}
