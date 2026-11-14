package assembly

import (
	"strings"
	"testing"
)

// Every surface table must name only catalog tools, and a tool may appear at
// most once per surface (the registry rejects duplicates at runtime).
func TestToolsetTablesAreConsistent(t *testing.T) {
	surfaces := map[string][][]Entry{
		"tui":   {TUIEarly, TUICore, Delegation(SurfaceTUI, true), MCPBridge, TUILate},
		"batch": {Batch, Delegation(SurfaceTUI, false)},
		"web":   {Web, Delegation(SurfaceWeb, true)},
		"child": {childSet},
	}
	for surface, tables := range surfaces {
		seen := map[string]bool{}
		for _, table := range tables {
			for _, e := range table {
				if _, ok := catalog[e.Tool]; !ok {
					t.Errorf("%s: unknown tool %q", surface, e.Tool)
				}
				if seen[e.Tool] {
					t.Errorf("%s: %q listed twice", surface, e.Tool)
				}
				seen[e.Tool] = true
			}
		}
	}
}

func TestChildToolsetIsAllAlwaysOn(t *testing.T) {
	reg := ChildToolset(t.TempDir())
	if reg.Len() != len(childSet) {
		t.Fatalf("child registry has %d tools, want %d", reg.Len(), len(childSet))
	}
	visible := "," + strings.Join(reg.VisibleNames(), ",") + ","
	for _, e := range childSet {
		if !strings.Contains(visible, ","+e.Tool+",") {
			t.Errorf("%s not always-on in the darwin child registry", e.Tool)
		}
	}
}

func TestBuildSystemPromptLayerOrder(t *testing.T) {
	got := BuildSystemPrompt(PromptSpec{
		Base:          "BASE",
		ModelProfile:  "PROFILE",
		Project:       "PROJECT",
		User:          "USER",
		SurfaceLayers: []string{"", "SURFACE"},
		AfterGoal:     []string{"AFTER"},
	})
	if want := "BASE\n\nPROFILE\n\nPROJECT\n\nUSER\n\nSURFACE\n\nAFTER"; got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}
