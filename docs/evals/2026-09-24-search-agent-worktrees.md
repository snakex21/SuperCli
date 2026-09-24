# Keep agent worktree copies out of ordinary repository search

## Evidence

In the recent Qwen USOS session, broad search_code calls returned only paths under .claude/worktrees/agent-... . A 25-hit Linux query and a 15-hit documentation query both exhausted their result caps inside another checkout before reaching the active project's files.

Read-only replay against the current USOS tree reproduced this with pre-change source files supplied through a Go overlay:

| Saved query | Worktree hits before | Worktree hits after | Result bytes before -> after |
| --- | ---: | ---: | ---: |
| (?i)linux, max=25 | 25/25 | 0/25 | 8221 -> 7171 |
| Documentation query, include=*.md, max=15 | 15/15 | 0/15 | 2348 -> 1718 |

The replay explicitly used context=0, which preserves the location-only rendering of these capped searches. No model was called, and no USOS file was changed. Results after the change point to ARCHITECTURE.md, BOOT_FLOW.md and other files in the active checkout. This establishes better source selection, not a measured reduction in model turns or proof that every returned hit answers the user's question.

## Change

The shared subtree policy now excludes the specific .claude/worktrees directory during searches from an ancestor. It does not exclude .claude settings/skills, normal source directories called worktrees, or other nested repositories. It does not delete or modify any checkout.

Explicitly setting path to the worktrees directory, a checkout below it, or a file inside it remains supported. The Go scanner prunes before reading directory contents; ripgrep gets a case-insensitive exclusion and also filters records relative to the requested root. When the selected root is already inside a worktree, that global rg exclusion is omitted so it cannot reject the explicitly requested files; descendant record filtering still applies.

Filename discovery, bounded workspace sampling, tree listings and the manifest walker share the policy. list_dir keeps an excluded-directory row marked not expanded. Both GUI and CLI/TUI use these tools, regardless of the selected model/provider. No prompt/schema text, helper model call, background index or provider transport changed.

## Tool benchmark

Windows / Ryzen 7 5800X3D; built-in Go scanner; 32 synthetic worktree copies with eight files each, plus one current source file. Each copied file has 256 unrelated lines. Both versions return the same current source hit. Three samples of ten iterations; fixture creation excluded.

| Median | Before | After |
| --- | ---: | ---: |
| Search time | 20.835 ms | 0.327 ms |
| Allocated bytes | 446796 | 116116 |
| Allocations | 2517 | 75 |

This measures local search in a workspace with many redundant copies. It does not imply a comparable speedup of the whole agent or of workspaces without such copies. Replay timings were single samples and are retained as observations, not a latency benchmark.

## Validation

- Active source survives a low hit cap despite numerous matching lines in a clone.
- Configuration/skills and ordinary worktrees-named source directories stay visible.
- Explicit worktree directory, checkout and file roots work with both the Go scanner and actual ripgrep 15.2.0.
- Case-insensitive directory recognition and nested copy filtering.
- Shared filename lookup, bounded workspace sample and tree-listing behavior.
- Full go test -timeout 90s ./... with SUPERCLI_TEST_RG set passed.
- go vet on search, files, preflight and manifest passed.

Artifacts: .tmp/search-agent-worktrees/ contains source snapshots, before-overlay.json, replay source and before/after outputs, benchmark results and validation logs.
