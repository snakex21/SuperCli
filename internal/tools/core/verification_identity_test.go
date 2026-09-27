package core

import (
	"reflect"
	"testing"
)

func TestVerificationKeyIgnoresOnlyPlainGoTestVerbosity(t *testing.T) {
	command := []string{"go", "test", "./..."}
	env := []string{"CHECK_MODE=full"}
	base := VerificationCommandKey(command, "repo", env)
	for _, verbose := range [][]string{
		{"go", "test", "./...", "-v"}, {"go", "test", "-v", "./..."}, {"go", "test", "-v=true", "./..."},
		{"go", "test", "./...", "-v=false"}, {"go", "test", "-test.v", "./..."}, {"go", "test", "-test.v=true", "./..."},
		{"go", "test", "./...", "-test.v=false"},
	} {
		original := append([]string(nil), verbose...)
		if got := VerificationCommandKey(verbose, "repo", env); got != base {
			t.Fatalf("verbosity differs: %q", verbose)
		}
		if !reflect.DeepEqual(verbose, original) {
			t.Fatal("command argv mutated")
		}
		if CommandKey(verbose, "repo", env) == CommandKey(command, "repo", env) {
			t.Fatal("exact execution identity conflated different output")
		}
	}
	if VerificationCommandKey([]string{"go", "test", "./pkg", "-v"}, "repo", env) == base {
		t.Fatal("different package set merged")
	}
	for _, other := range [][]string{
		{"go", "test", "./...", "-run", "Tiny", "-v"},
		{"go", "test", "./...", "-short", "-v"}, {"go", "test", "./...", "-race", "-v"},
		{"go", "test", "./...", "-count=0", "-v"}, {"go", "test", "./...", "-list", "-v"},
		{"go", "test", "./...", "-args", "-v"}, {"go", "test", "./...", "--", "-v"},
		{"go", "test", "-vet", "off", "./...", "-v"}, {"go", "test", "-unknown", "-v", "./..."},
		{"go", "test", "-run", "-v", "./..."}, {"go", "test", "./...", "-v=bad"},
		{"go", "build", "./...", "-v"}, {"custom-go", "test", "./...", "-v"},
		{"cmd", "/c", "go test ./... -v"},
	} {
		if VerificationCommandKey(other, "repo", env) == base {
			t.Fatalf("different verification scope merged: %q", other)
		}
		if !reflect.DeepEqual(verificationCommand(other), other) {
			t.Fatalf("unknown option was normalized: %q", other)
		}
	}
	if VerificationCommandKey(command, "other", env) == base || VerificationCommandKey(command, "repo", []string{"CHECK_MODE=quick"}) == base {
		t.Fatal("different directory/environment merged")
	}
	if VerificationCommandKey([]string{"go", "test", "./..."}, "repo", nil) != CommandKey([]string{"go", "test", "./..."}, "repo", nil) {
		t.Fatal("plain command changed its identity")
	}
}

func TestVerificationVerbosityKeepsExecutableIdentity(t *testing.T) {
	for _, exe := range []string{"/usr/local/go/bin/go", "C:\\Go\\bin\\go.exe"} {
		if VerificationCommandKey([]string{exe, "test", ".", "-v"}, "repo", nil) != VerificationCommandKey([]string{exe, "test", "."}, "repo", nil) {
			t.Fatalf("explicit executable not supported: %s", exe)
		}
		if VerificationCommandKey([]string{exe, "test", "."}, "repo", nil) == VerificationCommandKey([]string{"go", "test", "."}, "repo", nil) {
			t.Fatal("different executables merged")
		}
	}
}
