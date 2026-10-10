package webgui

import (
	"context"
	"errors"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
)

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
