package config

// SamplingConf is the `[sampling]` table of config.toml: the sampling
// parameters SuperCli forwards to the model server. Every field is a
// pointer, and that is the whole point — an absent key stays nil and is
// never sent, so the server keeps applying its own defaults. SuperCli
// invents no sampling values, so an empty config produces exactly the
// request it produced before this table existed.
//
//	[sampling]
//	temperature = 0.6
//	top_p = 0.95
//	top_k = 40
//	min_p = 0.05
//	repeat_penalty = 1.05
//	presence_penalty = 0.0
//	frequency_penalty = 0.0
//	seed = 1234
//
// Reach: the values land in a process-global default read by every
// provider constructor, so they also cover the delegated `task_model`
// worker, the `compact_model` summarizer, draft/council models and any
// provider built after a /model swap. A separate per-task table would
// only duplicate that reach, so there is deliberately none.
//
// Portability: temperature, top_p, presence_penalty, frequency_penalty
// and seed are standard OpenAI chat-completions parameters; top_k, min_p
// and repeat_penalty are llama.cpp/LM Studio extensions and are only
// emitted for local/private hosts. Anthropic receives temperature,
// top_p and top_k only (and nothing at all while extended thinking is
// on, which fixes the sampling server-side).
type SamplingConf struct {
	Temperature      *float64 `toml:"temperature"`
	TopP             *float64 `toml:"top_p"`
	TopK             *int     `toml:"top_k"`
	MinP             *float64 `toml:"min_p"`
	PresencePenalty  *float64 `toml:"presence_penalty"`
	FrequencyPenalty *float64 `toml:"frequency_penalty"`
	RepeatPenalty    *float64 `toml:"repeat_penalty"`
	Seed             *int64   `toml:"seed"`
}

// IsZero reports whether nothing was configured.
func (s SamplingConf) IsZero() bool {
	return s.Temperature == nil && s.TopP == nil && s.TopK == nil &&
		s.MinP == nil && s.PresencePenalty == nil && s.FrequencyPenalty == nil &&
		s.RepeatPenalty == nil && s.Seed == nil
}

// mergeSampling overlays the set fields of src onto dst, field by field,
// so a project config can raise temperature without discarding the
// global config's top_p.
func mergeSampling(dst *SamplingConf, src SamplingConf) {
	if src.Temperature != nil {
		dst.Temperature = src.Temperature
	}
	if src.TopP != nil {
		dst.TopP = src.TopP
	}
	if src.TopK != nil {
		dst.TopK = src.TopK
	}
	if src.MinP != nil {
		dst.MinP = src.MinP
	}
	if src.PresencePenalty != nil {
		dst.PresencePenalty = src.PresencePenalty
	}
	if src.FrequencyPenalty != nil {
		dst.FrequencyPenalty = src.FrequencyPenalty
	}
	if src.RepeatPenalty != nil {
		dst.RepeatPenalty = src.RepeatPenalty
	}
	if src.Seed != nil {
		dst.Seed = src.Seed
	}
}
