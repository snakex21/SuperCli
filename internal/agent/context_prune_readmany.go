package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// readManyStatusForPrune preserves the tool's generated batch summary, not
// arbitrary text in a numbered source excerpt. Missing/malformed framing stays
// unknown. The 12-item bound matches read_many's public batch limit.
func readManyStatusForPrune(content, storedHandle string) string {
	body := pruneStructuredBody(content, storedHandle)
	body, failed := strings.CutPrefix(body, "error: ")
	if !strings.HasPrefix(body, "== [1] ") {
		return ""
	}
	last := body[strings.LastIndexByte(body, '\n')+1:]
	counts, ok := strings.CutPrefix(last, "[read_many: ")
	if !ok {
		return ""
	}
	good, bad, ok := strings.Cut(counts, " ok, ")
	if !ok || !strings.HasSuffix(bad, " failed]") {
		return ""
	}
	bad = strings.TrimSuffix(bad, " failed]")
	okCount, e1 := strconv.Atoi(good)
	failedCount, e2 := strconv.Atoi(bad)
	if e1 != nil || e2 != nil || okCount < 0 || okCount > 12 ||
		failedCount < 0 || failedCount > 12 || okCount+failedCount < 1 ||
		okCount+failedCount > 12 || strconv.Itoa(okCount) != good || strconv.Itoa(failedCount) != bad {
		return ""
	}
	if failed && okCount != 0 {
		return ""
	}
	return fmt.Sprintf(", ok=%d, failed=%d", okCount, failedCount)
}
