package mediagen

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/system/config"
)

// A default_parameters key missing from allowed_parameters used to fail every
// call with "parameter is not configured" although the model never sent it.
func TestDefaultParameterOutsideAllowlistIsIgnoredWithNote(t *testing.T) {
	var requests atomic.Int32
	tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["quality"]; ok || body["size"] != "1024x1024" {
			t.Errorf("body=%v", body)
		}
		imageResponse(w, tinyPNG)
	})
	tool.cfg.AllowedParameters = []string{"size"}
	tool.cfg.DefaultParameters = map[string]any{"quality": "standard", "size": "1024x1024"}
	res, err := run(t, tool, `{"prompt":"test"}`)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
	if !strings.Contains(res.Text, "default_parameters quality were ignored") || !strings.Contains(res.Text, "allowed_parameters") {
		t.Fatalf("missing ignored-default note: %s", res.Text)
	}
	// A value the model sends itself stays strict.
	if _, err := run(t, tool, `{"prompt":"test","parameters":{"quality":"low"}}`); err == nil {
		t.Fatal("accepted a model-supplied parameter outside allowed_parameters")
	}
	if requests.Load() != 1 {
		t.Fatalf("rejected call reached the provider: requests=%d", requests.Load())
	}
}

func TestDefaultsWithinAllowlistProduceNoNote(t *testing.T) {
	tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) { imageResponse(w, tinyPNG) })
	tool.cfg.AllowedParameters = []string{"quality"}
	tool.cfg.DefaultParameters = map[string]any{"quality": "low"}
	res, err := run(t, tool, `{"prompt":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "were ignored") {
		t.Fatalf("unexpected note: %s", res.Text)
	}
}

// The definition, not recalled history, decides availability.
func TestEnabledToolDescriptionIsAuthoritativeAboutAvailability(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		on := &config.MediaGenerationProviderConf{Enabled: true}
		desc := newTool(t.TempDir(), kind, on).Spec().Description
		if !strings.Contains(desc, "configured and available now") || !strings.Contains(desc, "outdated") {
			t.Fatalf("%s: enabled description lacks availability note: %s", kind, desc)
		}
		off := newTool(t.TempDir(), kind, &config.MediaGenerationProviderConf{}).Spec().Description
		if strings.Contains(off, "available now") {
			t.Fatalf("%s: disabled tool claims availability: %s", kind, off)
		}
		if nilCfg := newTool(t.TempDir(), kind, nil).Spec().Description; strings.Contains(nilCfg, "available now") {
			t.Fatalf("%s: unconfigured tool claims availability", kind)
		}
	}
}
