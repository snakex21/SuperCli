package tui

import (
	"fmt"
	"regexp"
)

// The provider layer emits retry/limiter notices as fixed English wire text
// (llm.rateLimitNotice, llm.requestLimitNotice, agent completeWithStreamRetry).
// The web GUI localizes them by pattern (SYSTEM_TEXTS in 01-i18n.js); this is
// the TUI counterpart. Unknown notices are shown unchanged.
var localizedNotices = []struct {
	re  *regexp.Regexp
	key string
}{
	{regexp.MustCompile(`^(.+): rate limited \(HTTP 429\) — retrying in (\S+) \(retry (\S+)\)$`), "tui.notice.rate_limit_retry"},
	{regexp.MustCompile(`^(.+): (?:temporary )?provider error \(HTTP (\d+)\) — retrying in (\S+) \(retry (\S+)\)$`), "tui.notice.provider_retry"},
	{regexp.MustCompile(`^connection to the model dropped \(([\s\S]*)\) — retrying in (\S+) \(retry (\S+)\)$`), "tui.notice.connection_retry"},
	{regexp.MustCompile(`^Model connection unavailable — waiting (\S+) before retry (\S+)\.$`), "tui.notice.connection_wait"},
	{regexp.MustCompile(`^(.+): request limit (\d+)/min reached — waiting (\S+)$`), "tui.notice.request_limit"},
}

func (m Model) localizeNotice(text string) string {
	for _, n := range localizedNotices {
		match := n.re.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		format := m.tr(n.key)
		if format == n.key {
			return text
		}
		args := make([]any, 0, len(match)-1)
		for _, group := range match[1:] {
			args = append(args, group)
		}
		return fmt.Sprintf(format, args...)
	}
	return text
}
