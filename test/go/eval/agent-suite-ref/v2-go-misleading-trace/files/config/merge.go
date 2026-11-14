package config

// Merge overlays o on top of base.
//
// Contract: the result's Vars is never nil and is always a fresh map
// holding base's keys plus o's keys (o wins on conflicts). Neither
// base nor o is modified, and later writes to the result never leak
// back into base or o.
func Merge(base, o Config) Config {
	out := Config{Name: base.Name, Strict: base.Strict || o.Strict}
	if o.Name != "" {
		out.Name = o.Name
	}
	out.Vars = make(map[string]string, len(base.Vars)+len(o.Vars))
	for k, v := range base.Vars {
		out.Vars[k] = v
	}
	for k, v := range o.Vars {
		out.Vars[k] = v
	}
	return out
}
