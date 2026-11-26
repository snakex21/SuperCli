// Package workerstore adapts the session store to agent.WorkerStore, so
// delegated workers are resumable across runs and app restarts.
package workerstore

import (
	"context"

	"supercli/internal/agent"
	"supercli/internal/storage/session"
)

type Store struct{ S *session.Store }

func toRow(r agent.WorkerRecord) session.WorkerRow {
	return session.WorkerRow{SessionID: r.SessionID, ID: r.ID, Agent: r.Agent, Description: r.Description, Model: r.Model,
		BaseDir: r.BaseDir, Status: r.Status, LastError: r.LastError, Runs: r.Runs, CommandsRun: r.CommandsRun, ChecksRun: r.ChecksRun,
		Messages: r.Messages, UpdatedAt: r.UpdatedAt}
}

func toRecord(r session.WorkerRow) agent.WorkerRecord {
	return agent.WorkerRecord{SessionID: r.SessionID, ID: r.ID, Agent: r.Agent, Description: r.Description, Model: r.Model,
		BaseDir: r.BaseDir, Status: r.Status, LastError: r.LastError, Runs: r.Runs, CommandsRun: r.CommandsRun, ChecksRun: r.ChecksRun,
		Messages: r.Messages, UpdatedAt: r.UpdatedAt}
}

func (s Store) SaveWorker(ctx context.Context, rec agent.WorkerRecord) error {
	return s.S.SaveWorker(ctx, toRow(rec))
}

func (s Store) LoadWorker(ctx context.Context, sessionID, id string) (agent.WorkerRecord, bool, error) {
	row, ok, err := s.S.LoadWorker(ctx, sessionID, id)
	return toRecord(row), ok, err
}

func (s Store) ListWorkers(ctx context.Context, sessionID string) ([]agent.WorkerRecord, error) {
	rows, err := s.S.ListWorkers(ctx, sessionID)
	out := make([]agent.WorkerRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRecord(row))
	}
	return out, err
}

// ListWorkerSummaries lists saved workers without transcripts.
func (s Store) ListWorkerSummaries(ctx context.Context, sessionID string, activeOnly bool) ([]agent.WorkerRecord, error) {
	rows, err := s.S.ListWorkerSummaries(ctx, sessionID, activeOnly)
	out := make([]agent.WorkerRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRecord(row))
	}
	return out, err
}

// ListWorkerBriefs lists a session's saved workers without transcripts.
func (s Store) ListWorkerBriefs(ctx context.Context, sessionID string) ([]agent.WorkerRecord, error) {
	rows, err := s.S.ListWorkerBriefs(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]agent.WorkerRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, agent.WorkerRecord{SessionID: sessionID, ID: r.ID, Agent: r.Agent, Description: r.Description, Status: r.Status,
			CommandsRun: r.CommandsRun, ChecksRun: r.ChecksRun})
	}
	return out, nil
}

func (s Store) SetWorkerStatus(ctx context.Context, sessionID, id, status, reason string) error {
	return s.S.SetWorkerStatus(ctx, sessionID, id, status, reason)
}

// Attach enables persistence on a registry; ids already saved are reserved.
func Attach(ctx context.Context, workers *agent.WorkerRegistry, store *session.Store) error {
	if workers == nil || store == nil {
		return nil
	}
	ids, err := store.WorkerIDs(ctx)
	if err != nil {
		return err
	}
	workers.SetStore(Store{S: store}, ids)
	return nil
}
