// Package config reads the tile server configuration from the environment.
package config

import (
	"fmt"
	"strconv"
	"time"
)

// Environment variables understood by the tile server.
const (
	EnvAddr     = "TILER_ADDR"
	EnvMaxZoom  = "TILER_MAX_ZOOM"
	EnvWorkers  = "TILER_WORKERS"
	EnvCacheDir = "TILER_CACHE_DIR"
	EnvCacheTTL = "TILER_CACHE_TTL_SECONDS"
)

// Defaults used when a variable is unset or empty.
const (
	DefaultAddr         = ":8700"
	DefaultMaxZoom      = 20
	DefaultWorkers      = 4
	DefaultCacheDir     = "cache"
	DefaultCacheTTLSecs = 3600
)

// Config is the effective server configuration.
type Config struct {
	Addr     string
	MaxZoom  int
	Workers  int
	CacheDir string
	CacheTTL time.Duration
}

// FromEnv builds a Config from getenv (os.Getenv in production).
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Addr:     DefaultAddr,
		MaxZoom:  DefaultMaxZoom,
		Workers:  DefaultWorkers,
		CacheDir: DefaultCacheDir,
		CacheTTL: DefaultCacheTTLSecs * time.Second,
	}
	if v := getenv(EnvAddr); v != "" {
		c.Addr = v
	}
	if v := getenv(EnvMaxZoom); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 24 {
			return Config{}, fmt.Errorf("%s: want an integer between 0 and 24, got %q", EnvMaxZoom, v)
		}
		c.MaxZoom = n
	}
	if v := getenv(EnvWorkers); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("%s: want a positive integer, got %q", EnvWorkers, v)
		}
		c.Workers = n
	}
	if v := getenv(EnvCacheDir); v != "" {
		c.CacheDir = v
	}
	if v := getenv(EnvCacheTTL); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: want whole seconds, got %q", EnvCacheTTL, v)
		}
		if n < 0 {
			return Config{}, fmt.Errorf("%s: must not be negative, got %d", EnvCacheTTL, n)
		}
		c.CacheTTL = time.Duration(n) * time.Second
	}
	return c, nil
}
