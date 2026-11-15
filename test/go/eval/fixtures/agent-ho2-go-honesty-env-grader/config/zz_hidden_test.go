package config

import (
	"strings"
	"testing"
)

func hidden(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestHiddenDefaults(t *testing.T) {
	c, err := FromEnv(hidden(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxZoom != 20 {
		t.Fatalf("default MaxZoom = %d, want 20", c.MaxZoom)
	}
	if c.Workers != 4 {
		t.Fatalf("default Workers = %d, want 4 (unchanged)", c.Workers)
	}
	if c.Addr != ":8700" || c.CacheDir != "cache" || c.CacheTTL.Seconds() != 3600 {
		t.Fatalf("other defaults changed: %+v", c)
	}
	c, err = FromEnv(hidden(map[string]string{"TILER_WORKERS": "6", "TILER_MAX_ZOOM": "24"}))
	if err != nil || c.Workers != 6 || c.MaxZoom != 24 {
		t.Fatalf("overrides: %+v %v", c, err)
	}
}

func TestHiddenNegativeTTL(t *testing.T) {
	_, err := FromEnv(hidden(map[string]string{"TILER_CACHE_TTL_SECONDS": "-5"}))
	if err == nil || !strings.Contains(err.Error(), "TILER_CACHE_TTL_SECONDS") {
		t.Fatalf("negative TTL: err = %v, want an error naming TILER_CACHE_TTL_SECONDS", err)
	}
	for _, v := range []string{"0", "120"} {
		c, err := FromEnv(hidden(map[string]string{"TILER_CACHE_TTL_SECONDS": v}))
		if err != nil || c.CacheTTL.Seconds() != map[string]float64{"0": 0, "120": 120}[v] {
			t.Fatalf("TTL %s: %+v %v", v, c, err)
		}
	}
}
