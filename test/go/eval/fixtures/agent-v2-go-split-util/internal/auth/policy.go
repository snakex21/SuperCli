package auth

import (
	"time"

	"gateway/internal/util"
)

// MaxAge parses the configured session lifetime (default 7d).
func MaxAge(cfg string) time.Duration {
	if cfg == "" {
		cfg = "7d"
	}
	d, err := util.ParseDurationLoose(cfg)
	if err != nil {
		return 7 * 24 * time.Hour
	}
	return d
}
