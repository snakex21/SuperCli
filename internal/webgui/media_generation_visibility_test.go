package webgui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestMediaGenerationIsDiscoverableWithoutAlwaysOnSchemas(t *testing.T) {
	srv := newTestServer(t, false)
	_, registry, err := srv.eng.buildLoopWithSession(nil, nil, srv.eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"generate_image", "generate_video"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("missing %s", name)
		}
		for _, tool := range registry.Visible() {
			if tool.Name == name {
				t.Fatalf("%s adds a default prompt schema", name)
			}
		}
	}
}

// Regression: an explicitly enabled image generator stayed hidden behind
// tool_search, so the model never learned it existed and refused image
// requests. Enabled generators are advertised; disabled ones stay hidden.
func TestEnabledImageGenerationIsAdvertisedToTheModel(t *testing.T) {
	srv := newTestServer(t, false)
	config := `[media_generation.image]
enabled = true
provider = "openai"
base_url = "https://api.openai.com/v1"
model = "explicit-image-model"
api_key_env = "IMAGE_API_KEY"
`
	if err := os.WriteFile(filepath.Join(srv.eng.DataDir(), "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	_, registry, err := srv.eng.buildLoopWithSession(nil, nil, srv.eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := registry.VisibleNames()
	if !slices.Contains(names, "generate_image") {
		t.Fatalf("enabled generate_image is not advertised: %v", names)
	}
	if slices.Contains(names, "generate_video") {
		t.Fatalf("disabled generate_video must stay hidden: %v", names)
	}
}
