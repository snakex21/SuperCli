package execution

import (
	"testing"

	"supercli/internal/account/tier"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestResolveSeparatesCapabilityFromBackend(t *testing.T) {
	tests := []struct {
		name, model, base string
		wantTier          tier.Tier
		wantSmallPrompt   bool
		wantThin          bool
	}{
		{"large MoE local, 10B active", "qwen3.5-122b-a10b", "http://192.168.0.105:8086/v1", tier.Small, true, true},
		{"large MoE cloud, 10B active", "qwen3.5-122b-a10b", "https://example.com/v1", tier.Small, true, true},
		{"small cloud", "qwen3.5-9b", "https://example.com/v1", tier.Small, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(config.Config{Model: tt.model, BaseURL: tt.base}, config.TomlConfig{}, nil, false)
			if got.Capability != tt.wantTier || got.PromptSmall != tt.wantSmallPrompt || got.ThinTools != tt.wantThin {
				t.Fatalf("profile=%+v", got)
			}
			if got.ThinTools && !got.CatalogHoist {
				t.Fatalf("thin stable profile should enable measured catalog hoist: %+v", got)
			}
		})
	}
}

func TestResolveFullToolsEscapeHatch(t *testing.T) {
	got := Resolve(
		config.Config{Model: "qwen3.5-9b", BaseURL: "http://localhost:1234/v1"},
		config.TomlConfig{SmallFullTools: true}, nil, false,
	)
	if got.ThinTools {
		t.Fatal("small_full_tools=true must disable automatic schema thinning")
	}
	if got.CatalogHoist {
		t.Fatal("catalog hoist has no role when full tools are requested")
	}
}

func TestResolveStableToolsetOffDisablesAutomaticHoist(t *testing.T) {
	off := false
	got := Resolve(
		config.Config{Model: "qwen3.5-9b", BaseURL: "http://localhost:1234/v1"},
		config.TomlConfig{StableToolset: &off}, nil, false,
	)
	if !got.ThinTools || got.StableToolset || got.CatalogHoist {
		t.Fatalf("profile=%+v, want thin with unstable tools and no hoist", got)
	}
}

func TestResolveUsesCapabilityPricesAndOverrides(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "mystery", InputCost: 10, OutputCost: 20})
	got := Resolve(config.Config{Model: "mystery", BaseURL: "https://example.com/v1"}, config.TomlConfig{}, caps, false)
	if got.Capability != tier.Big {
		t.Fatalf("priced capability=%s, want big", got.Capability)
	}
	got = Resolve(config.Config{Model: "mystery", BaseURL: "https://example.com/v1"}, config.TomlConfig{
		ModelTiers: []config.ModelTierConf{{Pattern: "myst*", Tier: "small"}},
	}, caps, false)
	if got.Capability != tier.Small {
		t.Fatalf("override capability=%s, want small", got.Capability)
	}
}

func TestParallelLocalAndCloud(t *testing.T) {
	if on, warn := Parallel("http://10.0.0.2:8080/v1", nil); on || warn {
		t.Fatalf("local auto=(%v,%v), want false,false", on, warn)
	}
	if on, warn := Parallel("https://api.example.com/v1", nil); !on || warn {
		t.Fatalf("cloud auto=(%v,%v), want true,false", on, warn)
	}
	force := true
	if on, warn := Parallel("http://10.0.0.2:8080/v1", &force); !on || !warn {
		t.Fatalf("forced local=(%v,%v), want true,true", on, warn)
	}
}
