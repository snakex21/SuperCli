package llm

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Sampling carries the user's EXPLICIT sampling settings. Every field is
// a pointer on purpose: nil means "the user said nothing", and a nil
// field is never serialized into a request, so the server keeps applying
// its own defaults. SuperCli therefore invents no sampling values of its
// own — an empty config produces byte-for-byte the request it produced
// before this type existed.
//
// Not every backend understands every field. Temperature, TopP,
// PresencePenalty, FrequencyPenalty and Seed are standard OpenAI
// chat-completions parameters; TopK, MinP and RepeatPenalty are
// llama.cpp/LM Studio extensions and are only emitted for local/private
// hosts (same gate as cache_prompt), because cloud OpenAI answers HTTP
// 400 to unknown fields. Anthropic accepts temperature/top_p/top_k only.
type Sampling struct {
	Temperature      *float64
	TopP             *float64
	TopK             *int
	MinP             *float64
	PresencePenalty  *float64
	FrequencyPenalty *float64
	RepeatPenalty    *float64
	Seed             *int64
}

// IsZero reports whether nothing at all was set.
func (s Sampling) IsZero() bool {
	return s.Temperature == nil && s.TopP == nil && s.TopK == nil &&
		s.MinP == nil && s.PresencePenalty == nil && s.FrequencyPenalty == nil &&
		s.RepeatPenalty == nil && s.Seed == nil
}

// withoutLocalExtensions drops the llama.cpp/LM Studio-only fields,
// keeping the parameters every OpenAI-compatible endpoint understands.
func (s Sampling) withoutLocalExtensions() Sampling {
	s.TopK, s.MinP, s.RepeatPenalty = nil, nil, nil
	return s
}

// anthropicOnly keeps the three parameters the Anthropic Messages API
// accepts. Anthropic has no presence/frequency/repeat penalty, no min_p
// and no seed.
func (s Sampling) anthropicOnly() Sampling {
	return Sampling{Temperature: s.Temperature, TopP: s.TopP, TopK: s.TopK}
}

// responsesOnly keeps what the Responses API accepts.
func (s Sampling) responsesOnly() Sampling {
	return Sampling{Temperature: s.Temperature, TopP: s.TopP}
}

// String renders the settings as a stable, greppable one-liner. Used for
// telemetry only; "none" means a bare request (no sampling sent), which
// is exactly the case worth correlating with degenerate loops.
func (s Sampling) String() string {
	var b []string
	add := func(k string, v *float64) {
		if v != nil {
			b = append(b, k+"="+strconv.FormatFloat(*v, 'g', -1, 64))
		}
	}
	add("temperature", s.Temperature)
	add("top_p", s.TopP)
	if s.TopK != nil {
		b = append(b, "top_k="+strconv.Itoa(*s.TopK))
	}
	add("min_p", s.MinP)
	add("presence_penalty", s.PresencePenalty)
	add("frequency_penalty", s.FrequencyPenalty)
	add("repeat_penalty", s.RepeatPenalty)
	if s.Seed != nil {
		b = append(b, "seed="+strconv.FormatInt(*s.Seed, 10))
	}
	if len(b) == 0 {
		return "none"
	}
	return strings.Join(b, " ")
}

// samplingHolder boxes a Sampling so atomic.Value sees one concrete type.
type samplingHolder struct{ s Sampling }

// samplingDefault is the process-global sampling setting, populated once
// at startup from config.toml `[sampling]` (plus the SUPERCLI_LLM_*
// environment overrides). Providers built later in the session — a
// /model swap, a task_model worker, a compact_model summarizer, a
// failover hop — all read it, so the user's settings apply everywhere
// instead of only to the provider that happened to exist at boot.
var samplingDefault atomic.Value

// SetSamplingDefault installs the process-global sampling settings.
func SetSamplingDefault(s Sampling) { samplingDefault.Store(samplingHolder{s}) }

// SamplingDefault returns the process-global sampling settings; the zero
// value means nothing was configured.
func SamplingDefault() Sampling {
	if h, ok := samplingDefault.Load().(samplingHolder); ok {
		return h.s
	}
	return Sampling{}
}

// resolveSampling picks the per-construction override when it carries
// anything, else the process-global default.
func resolveSampling(explicit Sampling) Sampling {
	if !explicit.IsZero() {
		return explicit
	}
	return SamplingDefault()
}

var samplingLogged sync.Map

// logSampling records, once per distinct combination, what sampling a
// backend is actually going to receive. This is the whole telemetry
// footprint: no new database column, no new file, one greppable line in
// supercli.log — enough to answer "was this run sampled bare?" after the
// fact, which is the correlation the loop-degeneration measurements need.
func logSampling(protocol, model string, s Sampling) {
	line := "sampling: protocol=" + protocol + " model=" + model + " " + s.String()
	if _, dup := samplingLogged.LoadOrStore(line, struct{}{}); dup {
		return
	}
	log.Print(line)
}
