package session

import (
	"context"
	"encoding/json"

	"supercli/internal/llm"
)

// ModelContextFromTranscript reuses an already decoded transcript during UI
// resume. It avoids a second full SQL read and JSON decode of every message.
func (s *Store) ModelContextFromTranscript(ctx context.Context, id string, msgs []llm.Message, seqs []int) ([]llm.Message, error) {
	var through int
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT through_seq, messages_json FROM session_context_projections WHERE session_id = ?", id).Scan(&through, &raw)
	var projected []llm.Message
	if err != nil || json.Unmarshal(raw, &projected) != nil || !validMessages(projected) {
		return s.externalizeModelImages(id, append([]llm.Message(nil), msgs...)), ctx.Err()
	}
	for i, msg := range msgs {
		if i < len(seqs) && seqs[i] > through {
			projected = append(projected, msg)
		}
	}
	return s.externalizeModelImages(id, projected), ctx.Err()
}
