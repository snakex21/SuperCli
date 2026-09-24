# Reasoning history and casual continuation — 2026-09-23

**Superseded policy:** the later [native reasoning evaluation](2026-09-23-native-reasoning.md)
keeps native continuation state by default. This earlier report measured smaller
requests, not agent quality; only duplicate legacy display-text replay remains off.

## Observed session

Read-only inspection of the latest Universal_Service_OS session confirmed
the screenshot's two turns:

| Prompt | Total time | Backend wait | Input | Output | Reasoning |
| --- | ---: | ---: | ---: | ---: | ---: |
| cześć | 5.920 s | 2.077 s | 1828 | 106 | 76 |
| narazie nie wiem właśnie zastanawiam się | 33.994 s | 24.689 s | 6218 | 236 | 83 |

Each turn used one model call and no tools or helper calls.
The second turn's context preparation took about 4.7 ms, so scanning and
preparing context were not the main measured delay. The model received
a larger request and also generated a longer answer.

## Causes and fix

1. The second casual utterance fell through to coordinator mode, loading
   a larger system prompt/tool set and the deferred repository preflight.
   A bounded rule now recognizes complete short statements of indecision,
   including spelling/punctuation variants. Any unrecognized remainder
   retains the coordinator fallback; project keywords still take precedence.
   No classifier inference or new prompt instruction is added.
2. Live assistant messages were already stripped, but optional reasoning
   replay was on by default. It is now off by default. Explicit legacy
   opt-in via KeepThinking or SUPERCLI_KEEP_THINKING=1/true/yes/on remains.
3. GUI continuations passed archived InitialMessages directly into the
   model history, reintroducing every saved reasoning block. Constructor
   and TUI /resume now share the same normalization of assistant text.

The live stream and archived transcript still include reasoning. Visible
answers, user text, tool output, call/result IDs and image parts survive.
No archive migration, provider-specific branch, or OpenCode Zen transport
change is required. Existing sessions benefit when loaded.

## Verification

Regression tests reproduced the bugs before the change:
- default replay into the next request;
- archived reasoning reintroduced by GUI constructor input;
- the exact casual utterance switched routes and consumed preflight early.

After the change, tests cover consecutive live turns, TUI-style resume,
GUI continuation after engine restart, archive preservation, tool/image
preservation, and the first actual project request restoring full tools
and deferred preflight. Explicit legacy opt-in remains covered separately.

Synthetic GUI fixture, locally estimated message tokens (excluding tool
schemas, not actual provider usage):

| Request | Before | After |
| --- | ---: | ---: |
| Greeting | 216 | 216 |
| Casual continuation | 1697 | 266 |

These numbers demonstrate smaller requests, not a guaranteed speed factor.
No live model timing was measured for this change; actual latency also
depends on generated length, backend cache and load.

Full validation: `go test ./...`, `go vet ./...` and `git diff --check` passed.
