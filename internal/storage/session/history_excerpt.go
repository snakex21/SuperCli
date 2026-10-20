package session

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// HistoryExcerpt retrieves one indexed transcript address, never a complete
// conversation. Native reasoning and image payloads are not returned as text.
type HistoryExcerpt struct {
	SessionID                      string
	Seq                            int
	Role, Name, Text               string
	CreatedAt                      time.Time
	Offset, TotalBytes, NextOffset int
}

func (s *Store) ReadHistoryExcerpt(ctx context.Context, sessionID string, seq, offset, maxBytes int) (HistoryExcerpt, error) {
	if s == nil || s.db == nil {
		return HistoryExcerpt{}, fmt.Errorf("history excerpt: session store unavailable")
	}
	if strings.TrimSpace(sessionID) == "" || seq <= 0 || offset < 0 || offset == int(^uint(0)>>1) {
		return HistoryExcerpt{}, fmt.Errorf("history excerpt: session_id, positive seq and nonnegative byte offset required")
	}
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	if maxBytes < utf8.UTFMax {
		return HistoryExcerpt{}, fmt.Errorf("history excerpt: max_bytes must be at least 4 for UTF-8 pagination")
	}
	if maxBytes > 32768 {
		maxBytes = 32768
	}
	// The composite (session_id,seq) index selects one row before JSON work.
	// Text can live in Parts on newer transcripts, while content is empty.
	// Decode visible parts inside SQLite, rather than loading encrypted state
	// or base64 images into Go just to read an old answer.
	const query = `WITH selected AS MATERIALIZED (
	 SELECT role, IFNULL(name,'') AS name, created_at,
	 IFNULL(content,'') AS plain,
	 CASE WHEN json_valid(parts_json) AND json_array_length(parts_json)>0 THEN
	   COALESCE((SELECT group_concat(json_extract(p.value,'$.Text'),char(10))
	     FROM json_each(parts_json) p WHERE json_extract(p.value,'$.Type')='text'),'')
	 ELSE '' END AS parts,
	 CASE WHEN json_valid(tool_calls_json) AND json_array_length(tool_calls_json)>0 THEN
	   char(10) || COALESCE((SELECT group_concat(json_extract(c.value,'$.ID') || ' ' || json_extract(c.value,'$.Name') || ' ' || json_extract(c.value,'$.Arguments'),char(10))
	     FROM json_each(tool_calls_json) c),'') ELSE '' END AS calls
	 FROM messages WHERE session_id=? AND seq=?
	), visible AS MATERIALIZED (
	 SELECT role,name,created_at,plain || CASE WHEN plain<>'' AND parts<>'' THEN char(10) ELSE '' END || parts || calls AS body FROM selected
	) SELECT role,name,created_at,length(CAST(body AS BLOB)),substr(CAST(body AS BLOB),?,?) FROM visible`
	out := HistoryExcerpt{SessionID: sessionID, Seq: seq, Offset: offset}
	var raw []byte
	var created int64
	if err := s.db.QueryRowContext(ctx, query, sessionID, seq, offset+1, maxBytes+4).Scan(&out.Role, &out.Name, &created, &out.TotalBytes, &raw); err != nil {
		return HistoryExcerpt{}, err
	}
	if offset > out.TotalBytes {
		return HistoryExcerpt{}, fmt.Errorf("history excerpt: offset exceeds message length (%d bytes)", out.TotalBytes)
	}
	if len(raw) > 0 && !utf8.RuneStart(raw[0]) {
		return HistoryExcerpt{}, fmt.Errorf("history excerpt: offset is inside a UTF-8 character; use the returned next_offset")
	}
	end := min(len(raw), maxBytes)
	for end < len(raw) && end > 0 && !utf8.RuneStart(raw[end]) {
		end--
	}
	out.Text = string(raw[:end])
	if offset+end < out.TotalBytes {
		out.NextOffset = offset + end
	}
	out.CreatedAt = time.Unix(0, created).UTC()
	return out, nil
}
