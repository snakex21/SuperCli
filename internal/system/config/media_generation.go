package config

// MediaGenerationConf is opt-in and independent of chat providers. There is
// deliberately no ambient-key, model or paid-provider fallback.
type MediaGenerationConf struct {
	Image *MediaGenerationProviderConf `toml:"image" json:"image,omitempty"`
	Video *MediaGenerationProviderConf `toml:"video" json:"video,omitempty"`
}

// MediaGenerationProviderConf is a trusted operator configuration. BaseURL is
// the API root (OpenAI: https://api.openai.com/v1; fal: https://queue.fal.run).
// Model is an explicit image model or fal model endpoint path. APIKeyEnv names
// an environment variable; credentials themselves are never stored here.
type MediaGenerationProviderConf struct {
	Enabled                  bool           `toml:"enabled" json:"enabled"`
	Provider                 string         `toml:"provider" json:"provider"`
	BaseURL                  string         `toml:"base_url" json:"base_url"`
	Model                    string         `toml:"model" json:"model"`
	APIKeyEnv                string         `toml:"api_key_env" json:"api_key_env"`
	AllowedParameters        []string       `toml:"allowed_parameters" json:"allowed_parameters,omitempty"`
	DefaultParameters        map[string]any `toml:"default_parameters" json:"default_parameters,omitempty"`
	AllowedDownloadHosts     []string       `toml:"allowed_download_hosts" json:"allowed_download_hosts,omitempty"`
	MaxBytes                 int64          `toml:"max_bytes" json:"max_bytes,omitempty"`
	TimeoutSeconds           int            `toml:"timeout_seconds" json:"timeout_seconds,omitempty"`
	PollIntervalMilliseconds int            `toml:"poll_interval_milliseconds" json:"poll_interval_milliseconds,omitempty"`
}

// Replace complete provider records. Field-wise merging could accidentally
// pair an inherited credential with a newly configured recipient.
func mergeMediaGeneration(dst *MediaGenerationConf, src MediaGenerationConf) {
	if src.Image != nil {
		dst.Image = src.Image
	}
	if src.Video != nil {
		dst.Video = src.Video
	}
}
