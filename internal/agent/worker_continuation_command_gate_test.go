package agent

import "testing"

// The local model emitted this valid package spelling in a live continuation.
// Rejecting it injected an extra model request that production would not need.
func TestContinuationEvalAcceptsRecordedPackageSpelling(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		command []string
		want    bool
	}{
		{"recorded verbose service", []string{"go", "test", "-v", "-run", "TestEmptyKeysSkipped|TestPublishCanonicalKeys", "./service/"}, true},
		{"queue trailing slash", []string{"go", "test", "./queue/"}, true},
		{"existing all packages", []string{"go", "test", "./...", "-v"}, true},
		{"parent path", []string{"go", "test", "../"}, false},
		{"outside package", []string{"go", "test", "./service/../../other"}, false},
		{"arbitrary package", []string{"go", "test", "./other/"}, false},
		{"tool override", []string{"go", "test", "./service/", "-toolexec=external"}, false},
		{"missing run expression", []string{"go", "test", "./service/", "-run"}, false},
		{"shell", []string{"cmd", "/c", "go test ./service/"}, false},
		{"modify test", []string{"gofmt", "-w", "service/empty_key_test.go"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := continuationEvalCommandAllowed(root, tc.command); got != tc.want {
				t.Fatalf("command=%q allowed=%v want=%v", tc.command, got, tc.want)
			}
		})
	}
}
