package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContextIntegrityAppendPreservesSourcesAndOwnership(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		message := MessageWithContextBlocks(RoleUser, []ContextBlock{{Source: SourceHuman, Text: "Proszę zachować API 🙂"}})
		if multipart {
			message.Parts = []ContentPart{{Type: PartTypeText, Text: message.Content}, {Type: PartTypeImage, Image: &ImageRef{ID: "fixture"}}}
			message.Content = ""
		}
		before, _ := json.Marshal(message)
		first := AppendContextText(message, SourceRuntime, "Repo state: fixture-A")
		firstSaved, _ := json.Marshal(first)
		second := AppendContextText(first, SourceRuntime, "Repo state: fixture-B")
		after, _ := json.Marshal(message)
		stillFirst, _ := json.Marshal(first)
		if string(before) != string(after) || string(firstSaved) != string(stillFirst) {
			t.Fatal("appending automatic context changed a prior snapshot")
		}
		usage := MeasureContext([]Message{second}, nil)
		if usage.Sources[SourceHuman].TextBytes != int64(len("Proszę zachować API 🙂")) || usage.Sources[SourceRuntime].TextBytes != int64(len("\n\nRepo state: fixture-A\n\nRepo state: fixture-B")) {
			t.Fatalf("runtime addon acquired human attribution: %+v", usage.Sources)
		}
		if !IsConversationUserTurn(second) {
			t.Fatal("appending an addon erased the real instruction boundary")
		}
	}
	mixed := MessageWithContextBlocks(RoleUser, []ContextBlock{{Source: SourceHuman, Text: "Fix <task-notification>."}, {Source: SourceRuntime, Text: "Automatic initial context."}})
	updated := AppendContextText(mixed, SourceRuntime, "Automatic later context.")
	if got := MeasureContext([]Message{updated}, nil).Sources[SourceHuman].TextBytes; got != int64(len("Fix <task-notification>.")) {
		t.Fatal("extending a mixed message lost its known human boundary")
	}
	mixed.Content = "changed " + mixed.Content
	updated = AppendContextText(mixed, SourceRuntime, "fresh")
	if MeasureContext([]Message{updated}, nil).Sources[ContextUnknown].TextBytes != int64(len(mixed.Content)) {
		t.Fatal("invalid old origin was presented as confirmed provenance")
	}
	if !strings.HasSuffix(updated.Content, "\n\nfresh") {
		t.Fatal("appending context changed the actual message bytes")
	}
}
