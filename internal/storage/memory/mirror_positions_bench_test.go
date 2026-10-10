package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Time the real durable Put and mirror verification, using the identical
// fixture against the original source and the narrowed position reader.
func BenchmarkMemoryMirrorPositionProjection(b *testing.B) {
	for _, c := range []struct {
		name         string
		count, bytes int
	}{
		{"short", 1000, 16}, {"task_log_sized", 200, 4000}, {"large_entries", 200, MaxEntryContentBytes},
	} {
		b.Run(c.name, func(b *testing.B) {
			body := strings.Repeat("context line for completed project work\n", (c.bytes+39)/40)
			if len(body) < c.bytes {
				body += strings.Repeat("x", c.bytes-len(body))
			}
			body = body[:c.bytes]
			s := seedMemoryWriteFixture(b, c.count, body)
			entry := Entry{ID: fmt.Sprintf("note-%05d", c.count-1), Scope: ScopeFact, Content: body, CreatedAt: time.Unix(1700000000, 0)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				// Different first byte forces a real mirror write without growing the store.
				if i%2 == 0 {
					entry.Content = "A" + body[1:]
				} else {
					entry.Content = "B" + body[1:]
				}
				if err := s.Put(entry); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
