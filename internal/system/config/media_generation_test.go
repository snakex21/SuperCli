package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMediaGenerationTOMLAndAtomicMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := `[media_generation.image]
enabled = true
provider = "openai"
base_url = "https://api.openai.com/v1"
model = "explicit-image-model"
api_key_env = "MY_IMAGE_KEY"
allowed_parameters = ["quality"]
max_bytes = 123456
timeout_seconds = 180
[media_generation.image.default_parameters]
quality = "low"
[media_generation.video]
enabled = true
provider = "fal"
base_url = "https://queue.fal.run"
model = "fal-ai/explicit/video"
api_key_env = "MY_VIDEO_KEY"
allowed_download_hosts = ["v3.fal.media"]
poll_interval_milliseconds = 2000
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaGeneration.Image == nil || got.MediaGeneration.Image.APIKeyEnv != "MY_IMAGE_KEY" || got.MediaGeneration.Image.DefaultParameters["quality"] != "low" {
		t.Fatalf("image=%+v", got.MediaGeneration.Image)
	}
	if got.MediaGeneration.Video == nil || got.MediaGeneration.Video.Provider != "fal" {
		t.Fatalf("video=%+v", got.MediaGeneration.Video)
	}
	previousVideo := got.MediaGeneration.Video
	mergeToml(&got, TomlConfig{MediaGeneration: MediaGenerationConf{Image: &MediaGenerationProviderConf{BaseURL: "https://new.example"}}})
	if got.MediaGeneration.Image.APIKeyEnv != "" || got.MediaGeneration.Image.Enabled {
		t.Fatal("inherited credentials or enabled flag onto replacement recipient")
	}
	if got.MediaGeneration.Video != previousVideo {
		t.Fatal("unrelated video config lost")
	}
	mergeToml(&got, TomlConfig{MediaGeneration: MediaGenerationConf{Video: &MediaGenerationProviderConf{Enabled: false}}})
	if got.MediaGeneration.Video.Enabled {
		t.Fatal("could not explicitly disable video")
	}
}
func TestMediaGenerationEmptyConfigHasNoProvider(t *testing.T) {
	got, err := LoadToml(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaGeneration.Image != nil || got.MediaGeneration.Video != nil {
		t.Fatal("default enabled media configuration")
	}
}
