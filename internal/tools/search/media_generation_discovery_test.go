package search

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/system/config"
	"supercli/internal/tools/mediagen"
)

// Regression: users ask for "image generation", "draw an image" or in Polish
// ("wygeneruj obrazek"); tool_search used to activate generate_image only for
// the literal "generate image", so agents answered that they cannot do it.
func TestToolSearcherFindsImageGenerationForOrdinaryWording(t *testing.T) {
	for _, query := range []string{"generate image", "image generation", "draw an image", "wygeneruj obrazek"} {
		t.Run(query, func(t *testing.T) {
			reg := NewRegistry()
			reg.MustRegister(mediagen.NewImage(t.TempDir(), &config.MediaGenerationProviderConf{Enabled: true}, nil).Spec())
			idx, err := NewInMemoryIndex()
			if err != nil {
				t.Fatal(err)
			}
			defer idx.Close()
			ts := NewToolSearcher(reg, idx)
			if err := ts.RebuildIndex(); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]string{"query": query})
			res, err := ts.execute(context.Background(), raw)
			if err != nil || res.Err != nil {
				t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
			}
			if !reg.IsVisible("generate_image") {
				t.Fatalf("query %q did not activate generate_image: %s", query, res.Text)
			}
		})
	}
}
