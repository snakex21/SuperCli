package app

import (
	"context"
	"errors"
	"log"
	"time"

	"supercli/internal/account/usagecost"
	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// openSessionStack opens the F13 session store (non-fatal on error)
// and returns a writer for the live session plus optional search_history.
// Caller must defer Close on the store when non-nil.
func openSessionStack(dataDir, sessionID, home, model string, registry *tools.Registry, billingConfigs ...config.TomlConfig) (*session.Store, agent.SessionWriter) {
	// F13: open the session store. Messages get persisted
	// as the loop emits them, and a FTS5 index on
	// messages.content keeps the search_history tool fast.
	// A failure here is non-fatal: search_history is
	// disabled, but the loop still runs in-memory.
	sessStore, sessErr := session.OpenStore(dataDir)
	if sessErr != nil {
		log.Printf("session store: %v (search_history disabled)", sessErr)
		return nil, nil
	}
	if err := recoverSessionMediaDeletes(sessStore, dataDir); err != nil {
		log.Printf("session media recovery: %v", errors.Join(err, sessStore.Close()))
		return nil, nil
	}
	if len(billingConfigs) > 0 {
		tc := billingConfigs[0]
		if err := sessStore.BackfillBilling(context.Background(), func(u session.UsageRecord) session.PriceSnapshot { return usagecost.FreezeQuote(tc, u, true) }); err != nil {
			log.Printf("billing migration: %v", errors.Join(err, sessStore.Close()))
			return nil, nil
		}
	}
	// Record a sessions row for this live session so it carries a
	// cwd (the Writer only touches the messages table). This is what
	// lets /resume filter sessions to the current project.
	if err := sessStore.EnsureSession(sessionID, home, model); err != nil {
		log.Printf("session: ensure row: %v", err)
	}
	// Opt-in tool: the model discovers it via
	// tool_search when it wants to recall prior
	// sessions. We do NOT MarkAlwaysOn.
	if registry != nil {
		registry.MustRegister(tools.NewSearchHistory(sessStore).Spec())
	}
	return sessStore, session.NewWriter(sessStore, sessionID)
}

// Opening a Store probes one reserved directory. Recovery takes the shared
// filesystem gate only when a durable delete intent is actually present.
func recoverSessionMediaDeletes(store *session.Store, dataDir string) error {
	pending, err := store.HasMediaDeleteRecovery()
	if err != nil || !pending {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gate, err := checkpoint.NewStoreGate(dataDir)
	if err != nil {
		return err
	}
	transaction, err := gate.Acquire(ctx)
	if err != nil {
		return err
	}
	return errors.Join(store.RecoverMediaDeletes(ctx), transaction.Close())
}
