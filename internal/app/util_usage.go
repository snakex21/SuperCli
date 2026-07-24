package app

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `supercli %s — portable AI CLI agent

Usage:
  supercli [flags]

Flags:
  --home PATH                     select the workspace/sandbox (also: $SUPERCLI_HOME)
  --data-dir PATH                 override instance data (also: $SUPERCLI_DATA_DIR)
  --provider P                    LLM provider: openai, responses, anthropic, codex, opencode, or echo
  --model M                       model id (default: $SUPERCLI_LLM_MODEL or gpt-4o-mini)
  --key K                         API key (overrides SUPERCLI_LLM_API_KEY)
  --base-url U                    base URL (overrides SUPERCLI_LLM_BASE_URL)
  --echo                          force echo provider (useful for offline testing)
  --debug                         verbose logging
  --status                        print credit usage + audit tail and exit
  --doctor                        run environment checks and exit
  --list-models                   print known model capabilities and exit (add --refresh to re-fetch)
  --refresh                       with --list-models, re-fetch /v1/models and re-probe
  --model-info ID                 print details for a single model and exit
  --max-credits-per-session N     cap total tokens per session (0 = no cap)
  --max-credits-per-day N         cap total tokens per UTC day (0 = no cap)
  --draft-mode MODE               F11 draft mode: off|always|balanced|critical (default off; opt-in)
  --draft-model ID                F11 draft model id (required to enable F11; no auto-pick)
  --resume                        resume the most recent session on startup (also: /resume in the TUI)
  --version                       print version and exit
  -h, --help                      show this help

Env vars:
  SUPERCLI_LLM_PROVIDER, SUPERCLI_LLM_API_KEY, SUPERCLI_LLM_BASE_URL,
  SUPERCLI_LLM_MODEL, SUPERCLI_LLM_TEMPERATURE, SUPERCLI_LLM_STREAM,
  SUPERCLI_LLM_TIMEOUT, SUPERCLI_DEBUG, SUPERCLI_HOME, SUPERCLI_DATA_DIR

Data is stored in a single portable supercli-data/ directory next to
this exact executable (override only with --data-dir or SUPERCLI_DATA_DIR).
--home and SUPERCLI_HOME change the workspace, never the instance data.
Nothing is written to %%APPDATA%% or the user home without consent.
`, version)
}
