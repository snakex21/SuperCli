package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Usage-limit cooldown (after 1.0.40). A 429 "usage_limit_reached" carried
// resets_in_seconds ≈ 134000 (~37 h), yet the coordinator started five new
// workers on the same model; all failed within two minutes. The reset time is
// parsed from the error, the model is blocked until then, and the task tool
// and resumes refuse with the reset time instead of starting doomed workers.

// usageCooldowns: lower-cased model name -> time the limit resets.
var usageCooldowns sync.Map

var (
	resetsInSecondsRe = regexp.MustCompile(`"?resets_in_seconds"?\s*[:=]\s*(\d+)`)
	resetsAtRe        = regexp.MustCompile(`"?resets_at"?\s*[:=]\s*"?(\d{9,})`)
)

// isUsageLimitText reports a subscription/usage limit (not a short rate limit).
func isUsageLimitText(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "usage_limit_reached") || strings.Contains(s, "usage limit") ||
		strings.Contains(s, "usage_limit_error") || strings.Contains(s, "subscription limit")
}

// usageLimitReset returns when a usage-limit error resets, if it says so.
func usageLimitReset(text string, now time.Time) (time.Time, bool) {
	if !isUsageLimitText(text) {
		return time.Time{}, false
	}
	if m := resetsAtRe.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil && sec > now.Unix() {
			return time.Unix(sec, 0), true
		}
	}
	if m := resetsInSecondsRe.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil && sec > 0 {
			return now.Add(time.Duration(sec) * time.Second), true
		}
	}
	return time.Time{}, false
}

// noteUsageLimit records a model's usage-limit reset from an error.
func noteUsageLimit(model string, err error, now time.Time) {
	if err == nil || strings.TrimSpace(model) == "" {
		return
	}
	if until, ok := usageLimitReset(err.Error(), now); ok {
		usageCooldowns.Store(strings.ToLower(model), until)
	}
}

// usageCooldown reports whether model is blocked by a usage limit at now.
func usageCooldown(model string, now time.Time) (time.Time, bool) {
	v, ok := usageCooldowns.Load(strings.ToLower(strings.TrimSpace(model)))
	if !ok {
		return time.Time{}, false
	}
	until := v.(time.Time)
	if !now.Before(until) {
		usageCooldowns.Delete(strings.ToLower(strings.TrimSpace(model)))
		return time.Time{}, false
	}
	return until, true
}

// usageLimitRefusal is the task/resume refusal while a model is blocked. The
// coordinator relays it in the user's language.
func usageLimitRefusal(model string, until, now time.Time) string {
	left := until.Sub(now).Round(time.Minute)
	return fmt.Sprintf("refused: usage limit: the worker model %s hit its usage limit; it resets at %s (in %s). "+
		"Do not start or resume workers on it until then; tell the user and suggest switching the worker model in settings.",
		model, until.Local().Format("2006-01-02 15:04"), formatLeft(left))
}

func formatLeft(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// shortProviderError turns a provider error with a JSON body into one short
// readable reason (status and message), never the raw JSON.
func shortProviderError(text string) string {
	text = strings.TrimSpace(text)
	open := strings.Index(text, "{")
	if open < 0 {
		return text
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	end := strings.LastIndex(text, "}")
	if end <= open || json.Unmarshal([]byte(text[open:end+1]), &body) != nil {
		return text
	}
	msg := body.Error.Message
	if msg == "" {
		msg = body.Message
	}
	if msg == "" {
		msg = body.Detail
	}
	if msg == "" {
		msg = body.Error.Type
	}
	if msg == "" {
		return text
	}
	prefix := strings.TrimSpace(strings.TrimRight(text[:open], " :"))
	if prefix != "" {
		return prefix + ": " + msg
	}
	return msg
}

// UsageLimit is one model blocked by a usage limit until Until.
type UsageLimit struct {
	Model string    `json:"model"`
	Until time.Time `json:"until"`
}

// UsageLimits lists the models currently blocked by a usage limit (expired
// entries are dropped), sorted by model.
func UsageLimits(now time.Time) []UsageLimit {
	var out []UsageLimit
	usageCooldowns.Range(func(k, v any) bool {
		if until := v.(time.Time); now.Before(until) {
			out = append(out, UsageLimit{Model: k.(string), Until: until})
		} else {
			usageCooldowns.Delete(k)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}
