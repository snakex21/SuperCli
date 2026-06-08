package llm

import "strings"

// Source identifies which loader populated a
// ModelInfo. Lower numeric value = lower priority
// (gets overridden by higher-priority sources).
//
// Priority order (F16):
//
//	seed (1) < catalog (2) < provider (3) < probe (4)
//
// `seed` is the embedded default; `catalog` is the
// user's edited <home>/.supercli/models.json;
// `provider` is /v1/models; `probe` is the first-use
// minimal request.
type Source int

const (
	// SourceUnknown means the field was never set
	// (e.g. literal `ModelInfo{}`).
	SourceUnknown Source = 0
	// SourceSeed is the embedded capabilities_seed.json.
	SourceSeed Source = 1
	// SourceCatalog is the user's <home>/.supercli/models.json.
	SourceCatalog Source = 2
	// SourceProvider is a /v1/models response.
	SourceProvider Source = 3
	// SourceProbe is a first-use minimal request.
	SourceProbe Source = 4
	// SourceOpencode is a model discovered from the
	// opencode headless gateway's /v1/models endpoint.
	// F15.
	SourceOpencode Source = 5
	// SourceExternal is a price fetched from an external
	// source (pricepertoken.com, OpenRouter, etc). F28a.
	SourceExternal Source = 6
	// SourceUser is a manually entered price by the user
	// (highest priority — always wins over everything).
	// F28a.
	SourceUser Source = 7
)

// String returns a lowercase identifier used in CLI
// output and (de)serialization.
func (s Source) String() string {
	switch s {
	case SourceSeed:
		return "seed"
	case SourceCatalog:
		return "catalog"
	case SourceProvider:
		return "provider"
	case SourceProbe:
		return "probe"
	case SourceOpencode:
		return "opencode"
	case SourceExternal:
		return "external"
	case SourceUser:
		return "user"
	default:
		return "unknown"
	}
}

// ParseSource is the inverse of String. Returns
// SourceUnknown and ok=false on an unknown value.
func ParseSource(s string) (Source, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "seed":
		return SourceSeed, true
	case "catalog":
		return SourceCatalog, true
	case "provider":
		return SourceProvider, true
	case "probe":
		return SourceProbe, true
	case "opencode":
		return SourceOpencode, true
	case "external":
		return SourceExternal, true
	case "user":
		return SourceUser, true
	default:
		return SourceUnknown, false
	}
}

// SourcePriority returns true if `s` is strictly
// higher priority than `other`. The merge code uses
// this to decide whether to overwrite an existing
// entry. Equal or lower-priority sources do NOT
// overwrite.
func (s Source) Overrides(other Source) bool {
	return int(s) > int(other)
}
