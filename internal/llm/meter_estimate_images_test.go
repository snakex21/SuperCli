package llm

import (
	"strings"
	"testing"
)

// Images and large tool outputs count toward the compaction trigger: an
// image was free for the estimator, so a screenshot-heavy session could
// overflow the window before compacting.
func TestEstimateCountsImagesAndLargeToolOutputs(t *testing.T) {
	text := Message{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "look at this"}}}
	withImages := text
	withImages.Parts = append(withImages.Parts, ContentPart{Type: PartTypeImage, Image: &ImageRef{ID: "a", Active: true}},
		ContentPart{Type: PartTypeImage, Image: &ImageRef{ID: "b", Active: true}}, ContentPart{Type: PartTypeImage, Image: &ImageRef{ID: "c"}})
	if got, want := EstimateActiveImageTokens([]Message{text, withImages}), 2*EstImageTokens; got != want {
		t.Fatalf("two sent images and one dormant marker cost %d image tokens, want %d", got, want)
	}
	output := strings.Repeat("BUILD SUCCESSFUL in 12s; task :compileJava UP-TO-DATE\n", 6000) // ~330 KB
	tool := Message{Role: RoleTool, ToolCallID: "c1", Content: output}
	if got := EstimateMessageTokens(tool); got < len(output)/4 {
		t.Fatalf("a %d-byte tool output estimates %d tokens", len(output), got)
	}
}
