package config

import "testing"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8700" || c.MaxZoom != 18 || c.Workers != 4 || c.CacheDir != "cache" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"TILER_WORKERS": "12", "TILER_MAX_ZOOM": "10", "TILER_CACHE_TTL_SECONDS": "60"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Workers != 12 || c.MaxZoom != 10 || c.CacheTTL.Seconds() != 60 {
		t.Fatalf("overrides = %+v", c)
	}
}

func TestBadZoom(t *testing.T) {
	if _, err := FromEnv(env(map[string]string{"TILER_MAX_ZOOM": "30"})); err == nil {
		t.Fatal("want error")
	}
}
