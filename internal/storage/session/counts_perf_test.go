package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func BenchmarkSessionMessageCounts(b *testing.B) {
	for _, size := range []int{12, 3000} {
		for _, kind := range []string{"plain", "mixed", "structured"} {
			b.Run(fmt.Sprintf("%d/%s", size, kind), func(b *testing.B) {
				s, err := OpenStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				defer s.Close()
				sess, err := s.Create(b.TempDir(), "fixture", "counts")
				if err != nil {
					b.Fatal(err)
				}
				tx, err := s.db.Begin()
				if err != nil {
					b.Fatal(err)
				}
				defer tx.Rollback()
				stmt, err := tx.Prepare(`INSERT INTO messages(session_id,seq,role,content,parts_json,tool_call_id,tool_calls_json,created_at) VALUES(?,?,?,?,?,?,?,1)`)
				if err != nil {
					b.Fatal(err)
				}
				defer stmt.Close()
				toolText := strings.Repeat("file contents for the observed tool result\n", 100)
				for i := 0; i < size; i++ {
					role, content, parts, callID, calls := "user", "question", "", "", ""
					switch i % 3 {
					case 1:
						role, content, callID = "tool", toolText, "call"
					case 2:
						role, content = "assistant", "answer"
					}
					if kind == "mixed" && i%3 == 2 {
						content = ""
						calls = `[{"ID":"call","Name":"read_file","Arguments":"{}"}]`
					}
					if kind == "structured" {
						role, content = "assistant", "answer"
						parts = `[{"Type":"reasoning","Reasoning":{"Format":"chat","Model":"fixture","Scope":"fixture","Data":{"reasoning_content":"a short reasoning block"}}}]`
						calls = `[{"ID":"call","Name":"read_file","Arguments":"{}"}]`
					}
					if _, err := stmt.Exec(sess.ID, i+1, role, content, parts, callID, calls); err != nil {
						b.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
				for _, mode := range []string{"reference", "current"} {
					b.Run(mode, func(b *testing.B) {
						lookup := s.ReadMessageCounts
						if mode == "reference" {
							lookup = s.readMessageCountsReference
						}
						b.ReportAllocs()
						b.ResetTimer()
						for b.Loop() {
							got, err := lookup(context.Background(), sess.ID)
							if err != nil || got.User+got.Assistant+got.Tool != size {
								b.Fatal(got, err)
							}
						}
					})
				}
			})
		}
	}
}

func BenchmarkSessionAppendWithCounts(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	sess, err := s.Create(b.TempDir(), "fixture", "append")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		row := Encoded{Role: "user", Content: "question"}
		if i%3 == 1 {
			row = Encoded{Role: "assistant", ToolCallsJSON: `[{"ID":"call","Name":"read_file","Arguments":"{}"}]`}
		}
		if i%3 == 2 {
			row = Encoded{Role: "tool", ToolCallID: "call", Content: strings.Repeat("tool output for observed file contents\n", 100)}
		}
		if err := s.AppendMessage(context.Background(), sess.ID, row); err != nil {
			b.Fatal(err)
		}
	}
}

func (s *Store) readMessageCountsReference(ctx context.Context, sessionID string) (MessageCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role,
  octet_length(content) > 0,
  IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,'')
  FROM messages WHERE session_id = ?`, sessionID)
	if err != nil {
		return MessageCounts{}, err
	}
	defer rows.Close()
	var out MessageCounts
	for rows.Next() {
		var row Encoded
		var hasContent bool
		if err := rows.Scan(&row.Role, &hasContent, &row.PartsJSON, &row.ToolCallID, &row.ToolCallsJSON); err != nil {
			return MessageCounts{}, err
		}
		if hasContent {
			row.Content = "x"
		}
		msg, err := row.ToMessage()
		if err != nil {
			continue
		}
		switch msg.Role {
		case llm.RoleUser:
			out.User++
		case llm.RoleAssistant:
			out.Assistant++
			out.ToolCalls += len(msg.ToolCalls)
		case llm.RoleTool:
			out.Tool++
		}
	}
	if err := rows.Err(); err != nil {
		return MessageCounts{}, err
	}
	return out, nil
}
