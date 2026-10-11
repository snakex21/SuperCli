package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOtherProjectPreviewPreservesWholeWordsAndStoredNotes(t *testing.T) {
	global := newStripTestStore(t)
	root := t.TempDir()
	other := filepath.Join(root, "OtherProject")
	description := "Reviewed the project structure and key files to provide architectural feedback. " +
		"Architecture decisions: zażółć ąęśń, keep application data portable and retain agreements."
	RefreshCard(global, other, description, "active")
	for _, cap := range []int{140, 300, 700} {
		brief := BuildBriefing(global, nil, filepath.Join(root, "CurrentProject"), cap)
		if !strings.Contains(brief, "OtherProject") || !strings.Contains(brief, "abbreviated notes") || EstimateTokens(brief) > cap || !utf8.ValidString(brief) {
			t.Fatalf("bad project preview at cap %d: %s", cap, brief)
		}
		if strings.Contains(brief, "architec…") || strings.Contains(brief, "architectura…") {
			t.Fatalf("preview cut a word: %s", brief)
		}
	}
	stored, ok := global.GetProjectCard(ProjectKey(other))
	if !ok || stored.Description != description {
		t.Fatalf("rendering changed original card: %+v", stored)
	}
}

func TestProjectCardPreviewKeepsUnicodeAndByteBudget(t *testing.T) {
	for _, description := range []string{"Krótka informacja: zażółć ąęśń.", strings.Repeat("Żółć architektura 日本語 ", 20)} {
		preview := projectCardPreview(description, 80)
		if len(preview) > 80 || !utf8.ValidString(preview) {
			t.Fatalf("invalid bounded preview: %q", preview)
		}
		if len(description) <= 80 && preview != description {
			t.Fatalf("complete short note was changed: %q", preview)
		}
	}
}

func TestOtherProjectsFallBackToNamesUnderTightBudget(t *testing.T) {
	global := newStripTestStore(t)
	root := t.TempDir()
	RefreshCard(global, filepath.Join(root, "Other"), strings.Repeat("important description ", 10), "active")
	brief := BuildBriefing(global, nil, filepath.Join(root, "Current"), 90)
	if !strings.Contains(brief, "- Other\n") || strings.Contains(brief, "important description") || EstimateTokens(brief) > 90 {
		t.Fatalf("tight preview did not preserve project name: %s", brief)
	}
}
