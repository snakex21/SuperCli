package config

import (
	"os"
	"strconv"
)

// Config holds gateway settings.
type Config struct {
	Addr string
	// RateLimitPerMinute is the sustained request rate allowed per API key.
	RateLimitPerMinute int
	// Burst is how many requests a key may send at once.
	Burst int
}

// Load reads settings from the environment.
func Load() Config {
	c := Config{Addr: ":8088", RateLimitPerMinute: 120, Burst: 20}
	if v, err := strconv.Atoi(os.Getenv("GATEWAY_RPM")); err == nil {
		c.RateLimitPerMinute = v
	}
	return c
}
