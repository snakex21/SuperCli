package webgui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

func TestWebLegacyRecallSelectsProjectConversationsBeforeLimit(t *testing.T) {
	for _, mode := range []string{"foreign", "current", "duplicates"} {
		t.Run(mode, func(t *testing.T) {
			home, other := t.TempDir(), t.TempDir()
			eng, err := NewEngine(echoConfig(), home, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			add := func(cwd string, texts ...string) string {
				t.Helper()
				sess, err := store.Create(cwd, "echo-test", "recall fixture")
				if err != nil {
					t.Fatal(err)
				}
				writer := session.NewWriter(store, sess.ID)
				for _, text := range texts {
					if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: text}); err != nil {
						t.Fatal(err)
					}
				}
				return sess.ID
			}
			var expected []string
			for i := 0; i < 4; i++ {
				marker := fmt.Sprintf("saveddecision%d", i)
				expected = append(expected, marker)
				add(home, "needle previously completed work with a specific verified project decision: "+marker, "Keep that decision when continuing work.")
			}
			spam := make([]string, 48)
			for i := range spam {
				spam[i] = "needle"
			}
			currentID := "new-session"
			switch mode {
			case "foreign":
				add(other, spam...)
			case "current":
				currentID = add(home, spam...)
			case "duplicates":
				spam = append(spam, "needle dominantdecision")
				add(home, spam...)
			}
			result := eng.relevantLegacySessions(context.Background(), home, "needle", currentID, 1600)
			if !strings.Contains(result, "saveddecision") {
				t.Fatalf("previous project work disappeared: %q", result)
			}
			want := 4
			if mode == "duplicates" {
				want = 3
				if !strings.Contains(result, "dominantdecision") {
					t.Fatalf("best matching session missing: %q", result)
				}
			}
			found := 0
			for _, marker := range expected {
				if strings.Contains(result, marker) {
					found++
				}
			}
			if found != want {
				t.Fatalf("got %d different prior decisions, want %d: %q", found, want, result)
			}
			if currentID != "new-session" && strings.Contains(result, shortSessionID(currentID)) {
				t.Fatal("current session entered cross-session context")
			}
		})
	}
}

func TestWebLegacyRecallPreservesWorkspaceAliasesAndContextBudget(t *testing.T) {
	home := t.TempDir()
	nested := filepath.Join(home, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	add := func(cwd, text string) string {
		t.Helper()
		sess, err := store.Create(cwd, "echo-test", "alias fixture")
		if err != nil {
			t.Fatal(err)
		}
		writer := session.NewWriter(store, sess.ID)
		if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: text}); err != nil {
			t.Fatal(err)
		}
		if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleAssistant, Content: "Earlier decision confirmed."}); err != nil {
			t.Fatal(err)
		}
		return sess.ID
	}
	alias := home + string(filepath.Separator) + "."
	if runtime.GOOS == "windows" {
		alias = strings.ToUpper(filepath.ToSlash(alias))
	}
	add(alias, "needle savedaliasdecision")
	add(nested, "needle foreignprojectdecision")
	current := add(home, "needle currentconversationdecision")
	result := eng.relevantLegacySessions(context.Background(), home, "needle", current, 420)
	if !strings.Contains(result, "savedaliasdecision") {
		t.Fatalf("workspace alias lost: %q", result)
	}
	for _, excluded := range []string{"foreignprojectdecision", "currentconversationdecision"} {
		if strings.Contains(result, excluded) {
			t.Fatalf("excluded conversation leaked: %q", result)
		}
	}
	if memory.EstimateTokens(result) > 440 {
		t.Fatalf("context budget unexpectedly grew: %d tokens", memory.EstimateTokens(result))
	}
	if got := eng.relevantLegacySessions(context.Background(), home, "needle", current, 1); got != "" {
		t.Fatalf("impossible budget still emitted context: %q", got)
	}
}
