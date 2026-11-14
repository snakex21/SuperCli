// Package config holds template settings.
package config

// Config configures rendering.
type Config struct {
	Name   string
	Vars   map[string]string
	Strict bool
}

// Base returns the built-in defaults.
func Base() Config {
	return Config{Name: "site", Vars: map[string]string{"brand": "Acme", "year": "2026"}}
}
