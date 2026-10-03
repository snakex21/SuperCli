package llm

// ProviderModelName returns the model id rather than a transport display label.
// In particular, adding/removing Codex accounts is not a model migration.
func ProviderModelName(p Provider) string {
	if p == nil {
		return ""
	}
	p = Unwrap(p)
	if named, ok := p.(interface{ ModelName() string }); ok {
		return named.ModelName()
	}
	return p.Name()
}
