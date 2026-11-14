package render

import "tmplsvc/config"

// applyDefaults fills per-page variables before substitution.
func applyDefaults(c config.Config, page string) {
	c.Vars["page"] = page
	if _, ok := c.Vars["lang"]; !ok {
		c.Vars["lang"] = "en"
	}
}
