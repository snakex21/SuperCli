package llm

import (
	"strings"
	"testing"
)

func TestMessage_Validate(t *testing.T) {
	cases := []struct {
		name    string
		msg     Message
		wantErr bool
	}{
		{"user ok", Message{Role: RoleUser, Content: "hi"}, false},
		{"system ok", Message{Role: RoleSystem, Content: "sys"}, false},
		{"assistant ok", Message{Role: RoleAssistant, Content: "ok"}, false},
		{"tool empty content ok", Message{Role: RoleTool, Name: "bash", ToolCallID: "call_1"}, false},
		{"user empty content", Message{Role: RoleUser, Content: ""}, true},
		{"unknown role", Message{Role: Role("wizard"), Content: "x"}, true},
		{"multimodal ok", Message{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
		}}, false},
		{"multimodal text only ok", Message{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
		}}, false},
		{"invalid part type", Message{Role: RoleUser, Parts: []ContentPart{
			{Type: PartType("foo")},
		}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.msg.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestContentPart_Validate(t *testing.T) {
	cases := []struct {
		name    string
		part    ContentPart
		wantErr bool
	}{
		{"text ok", ContentPart{Type: PartTypeText, Text: "x"}, false},
		{"text empty", ContentPart{Type: PartTypeText}, true},
		{"image URL ok", ContentPart{Type: PartTypeImage, Image: &ImageRef{URL: "http://x/y.png"}}, false},
		{"image data ok", ContentPart{Type: PartTypeImage, Image: &ImageRef{Data: "AAA", MediaType: "image/png"}}, false},
		{"image nil", ContentPart{Type: PartTypeImage}, true},
		{"image no url no data", ContentPart{Type: PartTypeImage, Image: &ImageRef{}}, true},
		{"image data no mediatype", ContentPart{Type: PartTypeImage, Image: &ImageRef{Data: "AAA"}}, true},
		{"empty type", ContentPart{}, true},
		{"unknown type", ContentPart{Type: PartType("audio")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.part.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestImageRef_AsDataURI(t *testing.T) {
	cases := []struct {
		name string
		img  ImageRef
		want string
	}{
		{"URL wins", ImageRef{URL: "http://x/y.png"}, "http://x/y.png"},
		{"data composed", ImageRef{Data: "AAAA", MediaType: "image/png"}, "data:image/png;base64,AAAA"},
		{"existing data URI kept", ImageRef{URL: "data:image/jpeg;base64,XYZ"}, "data:image/jpeg;base64,XYZ"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.img.AsDataURI()
			if got != c.want {
				t.Fatalf("AsDataURI = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMessage_HasImage(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want bool
	}{
		{"text only", Message{Role: RoleUser, Content: "x"}, false},
		{"text part only", Message{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "x"}}}, false},
		{"has image", Message{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "A", MediaType: "image/png"}},
		}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.msg.HasImage(); got != c.want {
				t.Fatalf("HasImage = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMessage_TextOnly(t *testing.T) {
	original := Message{
		Role:    RoleUser,
		Content: "main",
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "part1"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "A", MediaType: "image/png"}},
			{Type: PartTypeText, Text: "part2"},
		},
		Name: "user",
	}
	got := original.TextOnly()
	if got.HasImage() {
		t.Fatal("TextOnly still has image")
	}
	if got.Role != RoleUser || got.Name != "user" {
		t.Fatalf("role/name lost: %+v", got)
	}
	if !strings.Contains(got.Content, "main") ||
		!strings.Contains(got.Content, "part1") ||
		!strings.Contains(got.Content, "part2") {
		t.Fatalf("missing text: %q", got.Content)
	}
}

func TestMessage_TextOnly_EmptyOriginal(t *testing.T) {
	got := Message{Role: RoleAssistant, Content: "hi"}.TextOnly()
	if got.Content != "hi" {
		t.Fatalf("Content = %q, want hi", got.Content)
	}
}
