package webgui

import (
	"context"

	"supercli/internal/storage/session"
)

func (e *Engine) transcript(ctx context.Context, id string) ([]transcriptMsg, error) {
	store, err := e.sessionStore()
	if err != nil {
		return []transcriptMsg{}, nil
	}
	meta, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return nil, errSessionOutsideWorkspace
	}
	rows, err := store.ReadMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	return buildTranscript(ctx, store, id, rows)
}

func (e *Engine) transcriptPage(ctx context.Context, id string, beforeSeq, limit int) (transcriptPage, error) {
	store, err := e.sessionStore()
	if err != nil {
		return transcriptPage{Messages: []transcriptMsg{}}, nil
	}
	meta, err := store.Get(id)
	if err != nil {
		return transcriptPage{}, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return transcriptPage{}, errSessionOutsideWorkspace
	}
	rows, hasMore, err := store.ReadMessagesBefore(ctx, id, beforeSeq, limit)
	if err != nil {
		return transcriptPage{}, err
	}
	messages, err := buildTranscript(ctx, store, id, rows)
	if err != nil {
		return transcriptPage{}, err
	}
	cursor := 0
	if len(messages) > 0 {
		cursor = messages[0].Seq
	}
	return transcriptPage{Messages: messages, HasMore: hasMore, BeforeSeq: cursor}, nil
}

func buildTranscript(ctx context.Context, store *session.Store, id string, rows []session.Encoded) ([]transcriptMsg, error) {
	if len(rows) == 0 {
		return []transcriptMsg{}, nil
	}
	fromSeq, toSeq := 0, 0
	fromSeq, toSeq = rows[0].Seq, rows[len(rows)-1].Seq
	turnRows, err := store.ReadTurnSummariesRange(ctx, id, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	turns := make(map[int]session.TurnSummary, len(turnRows))
	for _, turn := range turnRows {
		turns[turn.AssistantSeq] = turn
	}
	attachments, err := store.ReadMessageAttachmentsRange(ctx, id, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	out := make([]transcriptMsg, 0, len(rows))
	for _, m := range rows {
		msg, err := m.ToMessage()
		if err != nil {
			return nil, err
		}
		textOnly := msg.TextOnly()
		item := transcriptMsg{
			Seq:         m.Seq,
			Role:        m.Role,
			Content:     textOnly.Content,
			Attachments: append([]string(nil), attachments[m.Seq]...),
			Name:        m.Name,
			ToolCallID:  msg.ToolCallID,
		}
		for _, call := range msg.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, transcriptToolCall{
				ID: call.ID, Name: call.Name, Arguments: call.Arguments,
			})
		}
		if turn, ok := turns[m.Seq]; ok {
			item.Turn = &transcriptTurn{
				ElapsedMS: turn.DurationMS, TokIn: turn.Input, TokOut: turn.Output,
				TokTotal: turn.Input + turn.Output, TokCached: turn.CachedInput,
				ReasoningTok: turn.Reasoning, ToolCalls: turn.ToolCalls,
				FileChanges: append([]session.FileChange(nil), turn.FileChanges...),
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// memoryList returns recent memory entries across both scopes
// (project + global). A scope filter of "" returns everything.
