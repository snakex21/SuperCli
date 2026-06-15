package tui

import "strings"

// isModelUnavailableErr reports whether a provider error means
// "this model id does not exist / was retired" rather than a
// transient network or auth failure. Used to turn the raw HTTP
// body into an actionable "pick another with /model" message.
func isModelUnavailableErr(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "model") {
		return false
	}
	for _, marker := range []string{
		"model_not_found",
		"does not exist",
		"not found",
		"has been deprecated",
		"decommissioned",
		"deactivated",
		"no longer supported",
		"invalid model",
		"unknown model",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}
