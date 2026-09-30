package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExternalSkillCatalogDoesNotLoadUnrelatedBodies(t *testing.T) {
	root := t.TempDir()
	mkSkillDir(t, root, "officecli-docx", "---\ndescription: Edit Word documents\n---\nold selected guidance")
	// A long line used to exceed Scanner's limit and break the entire catalog.
	mkSkillDir(t, root, "unrelated", "---\ndescription: Unrelated skill\n---\n"+strings.Repeat("x", 512<<10))
	d := &Discoverer{Sources: []Source{{Dir: root, Priority: 100}}}
	hits, err := d.Search("officecli docx", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("catalog blocked by unrelated body: hits=%v err=%v", hits, err)
	}
	for _, skill := range d.cached {
		if skill.Content != "" || skill.Frontmatter != nil {
			t.Fatalf("catalog retained body/header for %q", skill.Name)
		}
	}
	// The catalog is enough for matching; only the chosen file is opened again.
	selectedPath := filepath.Join(root, "officecli-docx", "SKILL.md")
	if err := os.WriteFile(selectedPath, []byte("---\ndescription: Edit Word documents\n---\nnew selected guidance"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "unrelated", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	applier := NewSkillApplier(d)
	result, err := applier.execute(context.Background(), json.RawMessage(`{"query":"officecli docx","auto":true}`))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "new selected guidance") || strings.Contains(result.Text, "old selected guidance") {
		t.Fatalf("selected body was not loaded lazily: %+v err=%v", result, err)
	}
	if !reflect.DeepEqual(applier.Applied(), []string{"officecli-docx"}) {
		t.Fatalf("loaded extra skills: %v", applier.Applied())
	}
}

func TestSkillMetadataHandlesQuotedValuesAndBodySeparators(t *testing.T) {
	root := t.TempDir()
	mkSkillDir(t, root, "code-review", "---\nname: 'code-review'\ndescription: \"Review code\"\ncategory: development\nrisk: low\ntags: [\"review\", 'code']\n---\n# Review\n\nUse this paragraph.\n---\nKeep this body separator.\n")
	d := &Discoverer{Sources: []Source{{Dir: root, Priority: 100}}}
	page, total, err := d.List("review", 0, 5)
	if err != nil || total != 1 || page[0].Name != "code-review" || page[0].Description != "Review code" || page[0].Risk != "low" || page[0].Category != "development" {
		t.Fatalf("metadata: %+v err=%v", page, err)
	}
	selected, err := d.Get("code-review")
	if err != nil || selected.Name != "code-review" || selected.Priority != 100 || !strings.Contains(selected.Content, "---\nKeep this body separator.") {
		t.Fatalf("selected skill: %+v err=%v", selected, err)
	}
	if !reflect.DeepEqual(selected.Tags, []string{"review", "code"}) {
		t.Fatalf("tags: %v", selected.Tags)
	}
	// A body horizontal rule must not start a new frontmatter block.
	path := filepath.Join(root, "plain", "SKILL.md")
	mkSkillDir(t, root, "plain", "# Plain\nFirst paragraph.\n---\nKeep this paragraph too.\n")
	plain, err := readSkill(path)
	if err != nil || !strings.Contains(plain.Content, "Keep this paragraph too.") {
		t.Fatalf("lost body after horizontal rule: %+v err=%v", plain, err)
	}
}

func TestSkillMetadataFallbackIsBounded(t *testing.T) {
	root := t.TempDir()
	mkSkillDir(t, root, "no-header", "# Heading\n"+strings.Repeat("x", 512<<10))
	path := filepath.Join(root, "no-header", "SKILL.md")
	metadata, err := readSkillMetadata(path)
	if err != nil || metadata.Content != "" || len(metadata.Description) > skillMetadataReadLimit {
		t.Fatalf("unbounded fallback: bytes=%d err=%v", len(metadata.Description), err)
	}
	selected, err := readSkill(path)
	if err != nil || len(selected.Content) < 512<<10 {
		t.Fatalf("selected long line failed: bytes=%d err=%v", len(selected.Content), err)
	}
}

func TestSkillAutoSelectionDoesNotGuessAmbiguousQueries(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		limit       int
		selected    string
	}{
		{"clear name tokens", "officecli docx", 5, "officecli-docx"},
		{"case insensitive exact", "OFFICECLI-DOCX", 5, "officecli-docx"},
		{"shared task", "code review", 5, ""},
		{"limit must not hide ambiguity", "code review", 1, ""},
		{"one generic term", "officecli", 5, ""},
		{"repetition is not confidence", "officecli officecli", 5, ""},
		{"unmatched query term", "officecli docx impossible", 5, ""},
		{"no result", "xyzmissing", 5, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"officecli-docx", "officecli-xlsx", "code-review-go", "code-review-python"} {
				mkSkillDir(t, root, name, "---\ndescription: "+name+" workflow\n---\nGUIDANCE:"+name)
			}
			applier := NewSkillApplier(&Discoverer{Sources: []Source{{Dir: root, Priority: 100}}})
			args, _ := json.Marshal(map[string]any{"query": tc.query, "auto": true, "limit": tc.limit})
			result, err := applier.execute(context.Background(), args)
			if err != nil || result.Err != nil {
				t.Fatalf("selection: %+v err=%v", result, err)
			}
			applied := applier.Applied()
			if tc.selected != "" {
				if !reflect.DeepEqual(applied, []string{tc.selected}) || !strings.Contains(result.Text, "GUIDANCE:"+tc.selected) {
					t.Fatalf("clear match not applied: %v %s", applied, result.Text)
				}
			} else if len(applied) != 0 || strings.Contains(result.Text, "GUIDANCE:") {
				t.Fatalf("ambiguous query applied a body: %v %s", applied, result.Text)
			}
		})
	}
}

func TestAutoBuiltinLoadsOnlySelectedSkill(t *testing.T) {
	dataDir := t.TempDir()
	writeTestBuiltinPack(t, dataDir)
	d := &Discoverer{Builtin: NewBuiltinPack(dataDir)}
	applier := NewSkillApplier(d)
	result, err := applier.execute(context.Background(), json.RawMessage(`{"query":"007","auto":true}`))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "careful security audit") {
		t.Fatalf("builtin selection: %+v err=%v", result, err)
	}
	cache := filepath.Join(dataDir, "cache", "builtin-skills")
	folders, err := os.ReadDir(cache)
	if err != nil || len(folders) != 1 {
		t.Fatalf("cache roots: %v err=%v", folders, err)
	}
	files, err := os.ReadDir(filepath.Join(cache, folders[0].Name(), "skills"))
	if err != nil || len(files) != 1 || files[0].Name() != "007" {
		t.Fatalf("unselected skills extracted: %v err=%v", files, err)
	}
}

func TestSkillMetadataReadsMultilineDescriptions(t *testing.T) {
	for _, marker := range []string{">", ">-", "|", "|-"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			mkSkillDir(t, root, "word", "---\ndescription: "+marker+"\n  Edit Word documents\n  and preserve formatting.\ntags: [docx]\n---\nApply these steps.")
			path := filepath.Join(root, "word", "SKILL.md")
			metadata, err := readSkillMetadata(path)
			if err != nil || !strings.Contains(metadata.Description, "preserve formatting") || len(metadata.Tags) != 1 {
				t.Fatalf("metadata: %+v err=%v", metadata, err)
			}
			selected, err := readSkill(path)
			if err != nil || selected.Description != metadata.Description || selected.Content != "Apply these steps." {
				t.Fatalf("body/header split failed: %+v err=%v", selected, err)
			}
		})
	}
}

func TestAutoSkillAcceptsNaturalQueryWithoutGuessingAmongPeers(t *testing.T) {
	root := t.TempDir()
	mkSkillDir(t, root, "project-review", "---\ndescription: Project review procedure for Go arithmetic code correctness\n---\nselected project procedure")
	d := &Discoverer{Sources: []Source{{Dir: root, Priority: 100}}}
	applier := NewSkillApplier(d)
	result, err := applier.execute(context.Background(), json.RawMessage(`{"query":"project code review procedure for Go functions","auto":true}`))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "selected project procedure") {
		t.Fatalf("natural query failed: %+v err=%v", result, err)
	}
	if len(applier.Applied()) != 1 {
		t.Fatal("clear query did not select one skill")
	}
}
