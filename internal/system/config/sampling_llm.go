package config

import "supercli/internal/llm"

// Resolve folds the two places a user can express sampling into the one
// value every provider constructor reads. Precedence follows the config
// hierarchy documented at the top of this package: the
// SUPERCLI_LLM_TEMPERATURE env/flag override (envTemperature, nil when
// unset) beats the `[sampling]` table of config.toml.
//
// Anything the user did not set stays nil, and nil fields are never
// serialized into a request, so an empty config produces exactly the
// request SuperCli produced before sampling pass-through existed.
//
// (internal/llm imports no other internal package, so depending on it
// from here cannot introduce an import cycle.)
func (s SamplingConf) Resolve(envTemperature *float64) llm.Sampling {
	out := llm.Sampling{
		Temperature:      s.Temperature,
		TopP:             s.TopP,
		TopK:             s.TopK,
		MinP:             s.MinP,
		PresencePenalty:  s.PresencePenalty,
		FrequencyPenalty: s.FrequencyPenalty,
		RepeatPenalty:    s.RepeatPenalty,
		Seed:             s.Seed,
	}
	if envTemperature != nil {
		out.Temperature = envTemperature
	}
	return out
}
