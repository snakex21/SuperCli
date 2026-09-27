package files

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

// A stale patch used to require a separate shell hash command in addition to
// reviewing the changed source. Reuse the snapshot hash from the rejection;
// another intervening edit must still reject the retry.
func TestPatchFileTool_StaleHashRecovery(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "LF", "\r\n": "CRLF"}[newline], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sample.go")
			current := "// źródło changed externally" + newline + "const Value = 1" + newline
			write := func(text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			assertUnchanged := func(want string) {
				t.Helper()
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("unexpected file write: %q, %v", got, err)
				}
			}
			write(current)
			reg := core.NewRegistry()
			for _, spec := range []core.Tool{NewPatchFile(dir).Spec(), NewReadLines(dir).Spec()} {
				reg.MustRegister(spec)
				reg.MarkAlwaysOn(spec.Name)
			}
			patch := func(hash, old string) core.Result {
				t.Helper()
				args, err := json.Marshal(map[string]string{
					"path": "sample.go", "base_hash": hash, "old": old, "new": "const Value = 2",
				})
				if err != nil {
					t.Fatal(err)
				}
				result, err := reg.Execute(t.Context(), "patch_file", args)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			rejectedHash := func(result core.Result, contents string) string {
				t.Helper()
				if result.Err == nil {
					t.Fatal("stale patch was accepted")
				}
				visible := reg.ModelResultContent("patch_file", result)
				match := regexp.MustCompile("current_hash=([0-9a-f]{64})").FindStringSubmatch(visible)
				if len(match) != 2 {
					t.Fatalf("model must not need another hash command: %s", visible)
				}
				sum := sha256.Sum256([]byte(contents))
				if match[1] != hex.EncodeToString(sum[:]) {
					t.Fatal("diagnostic hash does not identify the rejected file snapshot")
				}
				if !strings.Contains(visible, "nothing written") || !strings.Contains(visible, "review current contents") {
					t.Fatalf("missing rejection/review guidance: %s", visible)
				}
				return match[1]
			}

			hash := rejectedHash(patch("outdated-hash", "const Value = 1"), current)
			assertUnchanged(current)

			// The diagnostic is a snapshot, never a token that bypasses later
			// changes from the user, a formatter or another worker.
			current += "// another writer" + newline
			write(current)
			hash = rejectedHash(patch(hash, "const Value = 1"), current)
			assertUnchanged(current)

			read, err := reg.Execute(t.Context(), "read_lines", json.RawMessage(
				"{\"file\":\"sample.go\",\"from\":1,\"to\":10}"))
			if err != nil || read.Err != nil || !strings.Contains(read.Text, "another writer") {
				t.Fatalf("current source unavailable for review: %v %+v", err, read)
			}
			result := patch(strings.ToUpper(hash), "const Value = 1")
			if result.Err != nil || !strings.Contains(result.Text, "changed=true") {
				t.Fatalf("reviewed retry failed: %+v", result)
			}
			current = strings.Replace(current, "const Value = 1", "const Value = 2", 1)
			assertUnchanged(current)
		})
	}
}
