package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBriefingFiltersOldJunkAndDuplicatesWithoutChangingRecords(t *testing.T) {
	saver, project, global := newAutoSaverForTest(t)
	for i, text := range []string{"The user prefers Polish for communication.", "The user prefers to communicate in Polish.", "The user likes pewnie że tak że mi tak bezinteresownie pomagasz w moich problemach.", "The user likes coffee.", "The user dislikes coffee."} {
		if err := global.Put(Entry{ID: fmt.Sprint(i), Scope: ScopePreference, Content: text, Source: SourceAgent}); err != nil {
			t.Fatal(err)
		}
	}
	if err := project.Put(Entry{ID: "log-empty", Scope: ScopeTaskLog, Content: "Example_Project: NOTHING", Source: SourceAgent}); err != nil {
		t.Fatal(err)
	}
	RefreshCard(global, saver.ProjectPath, "NOTHING", "active")
	brief := BuildBriefing(global, project, saver.ProjectPath, 2000)
	if strings.Count(brief, "Polish") != 1 || strings.Contains(brief, "pewnie") || strings.Contains(brief, "NOTHING") || !strings.Contains(brief, "likes coffee") || !strings.Contains(brief, "dislikes coffee") {
		t.Fatalf("unclean briefing:\n%s", brief)
	}
	all, _ := global.List(ScopePreference, 100)
	if len(all) != 5 {
		t.Fatal("rendering deleted durable records")
	}
}

func TestConversationalFragmentsDoNotBecomePreferences(t *testing.T) {
	for _, text := range []string{"Lubię pewnie że tak że mi tak bezinteresownie pomagasz", "I like that you helped me with this", "Lubię że pomagasz mi z tym zadaniem"} {
		if got := ExtractUserFacts([]string{text}); len(got) != 0 {
			t.Fatalf("conversation saved: %v", got)
		}
	}
	saver, project, global := newAutoSaverForTest(t)
	saver.StoreSummary(context.Background(), "hello", func(context.Context, string) (string, error) {
		return "Any_Project: NOTHING\nUSER: The user likes pewnie że tak że mi tak bezinteresownie pomagasz.", nil
	})
	if logs, _ := project.Recent(ScopeTaskLog, 10); len(logs) != 0 {
		t.Fatal("qualified NOTHING saved")
	}
	if prefs, _ := global.Recent(ScopePreference, 10); len(prefs) != 0 {
		t.Fatal("generated conversation saved")
	}
	if emptyMemoryNote("Decision: do nothing until the user approves.") {
		t.Fatal("real prohibition lost")
	}
	if similarFact("The user likes dark mode.", "The user dislikes dark mode.") {
		t.Fatal("opposing facts merged")
	}
	if similarFact("Użytkownik lubi kawę.", "Użytkownik lubi gry.") {
		t.Fatal("different Polish facts merged")
	}
	if junkFact("The user prefers all applications to keep their data inside the application folder for portability.") {
		t.Fatal("long standing requirement filtered as chat")
	}
	if got := ExtractUserFacts([]string{"My name is How Smith"}); len(got) != 1 {
		t.Fatal("preference filtering changed explicit names")
	}
}
