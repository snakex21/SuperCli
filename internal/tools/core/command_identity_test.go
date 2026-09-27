package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCommandKeyEquivalentOverrides(t *testing.T) {
	base := CommandKey([]string{"go", "test", "./..."}, "repo/pkg", []string{"FIRST=1", "SECOND=2"})
	for _, env := range [][]string{
		{"SECOND=2", "FIRST=1"},
		{"FIRST=old", "SECOND=2", "FIRST=1"},
	} {
		if got := CommandKey([]string{"go", "test", "./..."}, "repo/pkg", env); got != base {
			t.Fatalf("equivalent overrides changed identity: %v", env)
		}
	}
	if CommandKey([]string{"go", "test"}, ".", nil) != CommandKey([]string{"go", "test"}, ".", []string{}) {
		t.Fatal("empty and omitted overrides differ")
	}
	for _, got := range [][32]byte{
		CommandKey([]string{"go", "test", "./..."}, "repo/pkg", []string{"FIRST=other", "SECOND=2"}),
		CommandKey([]string{"go", "test", "./..."}, "repo/elsewhere", []string{"FIRST=1", "SECOND=2"}),
		CommandKey([]string{"go", "test", "./other"}, "repo/pkg", []string{"FIRST=1", "SECOND=2"}),
	} {
		if got == base {
			t.Fatal("different command, directory or override shared an identity")
		}
	}
}

func TestCommandEnvironmentPlatformSemantics(t *testing.T) {
	input := []string{"Mode=old", "MODE=new", "WITH_EQUALS=a=b", "EMPTY="}
	windows := canonicalCommandEnv(input, true)
	unix := canonicalCommandEnv(input, false)
	if !reflect.DeepEqual(windows, []string{"empty=", "mode=new", "with_equals=a=b"}) {
		t.Fatalf("Windows last override: %v", windows)
	}
	if !reflect.DeepEqual(unix, []string{"EMPTY=", "MODE=new", "Mode=old", "WITH_EQUALS=a=b"}) {
		t.Fatalf("case-sensitive environment: %v", unix)
	}
	if !reflect.DeepEqual(input, []string{"Mode=old", "MODE=new", "WITH_EQUALS=a=b", "EMPTY="}) {
		t.Fatal("canonicalizing changed caller's execution arguments")
	}
}

func TestCommandKeyNeverEntersModelOrUIOutput(t *testing.T) {
	key := CommandKey([]string{"go", "test"}, ".", []string{"PRIVATE_FIXTURE=private-value"})
	result := Result{Text: "passed", CommandKey: &key}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "CommandKey") || strings.Contains(string(encoded), "private") {
		t.Fatalf("internal metadata leaked: %s", encoded)
	}
	if result.ModelContent() != "passed" {
		t.Fatal("execution metadata changed model context")
	}
}
