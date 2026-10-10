package webgui

import (
	"context"
	"strconv"

	"supercli/internal/agent"
	"supercli/internal/llm"
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
	rows, err := store.ReadTranscriptMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	return e.buildTranscript(ctx, store, id, rows)
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
	rows, hasMore, err := store.ReadTranscriptMessagesBefore(ctx, id, beforeSeq, limit)
	if err != nil {
		return transcriptPage{}, err
	}
	messages, err := e.buildTranscript(ctx, store, id, rows)
	if err != nil {
		return transcriptPage{}, err
	}
	cursor := 0
	if len(rows) > 0 {
		// Pages containing only legacy summaries still need an advancing cursor.
		cursor = rows[0].Seq
	}
	return transcriptPage{Messages: messages, HasMore: hasMore, BeforeSeq: cursor}, nil
}

func (e *Engine) buildTranscript(ctx context.Context, store *session.Store, id string, rows []session.TranscriptMessage) ([]transcriptMsg, error) {
	if len(rows) == 0 {
		return []transcriptMsg{}, nil
	}
	fromSeq, toSeq := 0, 0
	fromSeq, toSeq = rows[0].Seq, rows[len(rows)-1].Seq
	turnRows, err := store.ReadTurnSummariesRange(ctx, id, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	e.recoverCheckpointChanges(ctx, store, id, turnRows)
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
		if agent.IsLegacyCompactionSummary(msg) {
			continue
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
		if msg.Role == llm.RoleUser && m.MessageID > 0 {
			item.MessageID = strconv.FormatInt(m.MessageID, 10)
		}
		item.ToolImages = transcriptToolImagePreviews(id, msg, len(item.Attachments) != 0)
		item.ToolImageCarrier = len(item.ToolImages) != 0
		if !item.ToolImageCarrier && len(item.Attachments) == 0 {
			for _, part := range msg.Parts {
				if part.Type == llm.PartTypeImage && part.Image != nil {
					if token := sessionImagePreviewPath(id, part.Image.Path); token != "" {
						item.Attachments = append(item.Attachments, token)
					}
				}
			}
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

// Only an explicitly host-authored, image-only carrier is reparented. Mixed,
// genuine and legacy messages retain the existing attachment presentation.
func transcriptToolImagePreviews(sessionID string, msg llm.Message, realAttachments bool) []transcriptToolImage {
	if realAttachments || msg.Role != llm.RoleUser || msg.Content != "" || msg.Name != "" || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 {
		return nil
	}
	var images []transcriptToolImage
	for _, part := range msg.Parts {
		switch part.Type {
		case llm.PartTypeText:
			// Surrounding host text is covered by the explicit carrier marker,
			// never recognized by its words or an image name.
		case llm.PartTypeImage:
			if part.Image == nil || !part.Image.ToolOutputCarrier || part.Image.SourceToolCallID == "" {
				return nil
			}
			token := sessionImagePreviewPath(sessionID, part.Image.Path)
			if token == "" {
				return nil
			}
			images = append(images, transcriptToolImage{SourceCallID: part.Image.SourceToolCallID, Path: token})
		default:
			return nil
		}
	}
	return images
}

// memoryList returns recent memory entries across both scopes
// (project + global). A scope filter of "" returns everything.
