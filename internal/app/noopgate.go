// Turn-economy: the noop-gate for batch (-p) runs. When the config
// knob `noop_gate` is ON and the working tree fingerprint is
// identical to the one saved after the last successful run of the
// SAME prompt, the run is skipped entirely — zero LLM calls, exit 0.
//
// Only batch mode is gated. Interactive sessions are never gated:
// a user typing to the model wants the model. The gate is strictly
// fail-open — a missing manifest, an IO error, anything unclear
// means "run normally"; the gate can only ever save work, never
// block it.
package app

import (
	"path/filepath"
	"time"

	"supercli/internal/storage/memory"
	"supercli/internal/system/manifest"
)

// resolveNoopGate applies the config.toml tri-state: nil = built-in
// default (OFF), explicit true/false overrides.
func resolveNoopGate(override *bool) bool {
	return override != nil && *override
}

// noopManifestPath is where the manifest for (project root, prompt)
// lives: next to the rest of the per-project state under the data
// dir, keyed by the prompt hash so different operations never share
// a manifest.
func noopManifestPath(dataDir, home, prompt string) string {
	return filepath.Join(dataDir, "projects", memory.ProjectKey(home),
		"noop", manifest.OpKey(prompt)+".json")
}

// noopGateSkip reports whether the batch run for (home, prompt) can
// be skipped. Fail-open by construction: any error on load or
// compute returns false ("run normally").
func noopGateSkip(dataDir, home, prompt string) (manifest.Record, bool) {
	rec, err := manifest.Load(noopManifestPath(dataDir, home, prompt))
	if err != nil || rec.Hash == "" {
		return manifest.Record{}, false
	}
	// dataDir is excluded from the walk so state SuperCli itself
	// writes (sessions, logs, this manifest) never invalidates the
	// fingerprint when the data dir lives inside the project root
	// (portable mode).
	hash, err := manifest.Compute(home, dataDir)
	if err != nil || hash == "" || hash != rec.Hash {
		return manifest.Record{}, false
	}
	return rec, true
}

// noopGateSave records the post-run tree fingerprint for (home,
// prompt). Called only after a successful batch run. Errors are
// swallowed: failing to save just means the next run is not gated.
func noopGateSave(dataDir, home, prompt string) {
	hash, err := manifest.Compute(home, dataDir)
	if err != nil || hash == "" {
		return
	}
	op := prompt
	if len(op) > 200 {
		op = op[:200]
	}
	_ = manifest.Save(noopManifestPath(dataDir, home, prompt), manifest.Record{
		Hash: hash, UpdatedAt: time.Now().UTC(), Operation: op,
	})
}
