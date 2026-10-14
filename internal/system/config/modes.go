package config

import "strings"

// AdvisorDisabled reports advisor_model = "off": the user turned the
// advise:true second opinion off, so the task tool must not offer it.
func AdvisorDisabled(c TomlConfig) bool {
	return strings.EqualFold(strings.TrimSpace(c.AdvisorModel), "off")
}

// ReviewDisabled reports that post-work review is off: review_model is
// empty or "off" (case-insensitive, trimmed). Off means zero extra calls.
func ReviewDisabled(c TomlConfig) bool {
	v := strings.TrimSpace(c.ReviewModel)
	return v == "" || strings.EqualFold(v, "off")
}

// DarwinEnabled reports whether the darwin tool is offered to the model
// (config `darwin`; nil = off — Darwin runs N candidates, so it costs N×
// and must be opted into).
func DarwinEnabled(c TomlConfig) bool {
	return c.Darwin != nil && *c.Darwin
}
