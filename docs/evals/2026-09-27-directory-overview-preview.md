# Keep workspace roots visible in directory previews — 2026-09-27

## Recorded evidence

In the frozen completed GunMayhem session de105c910d16e47e, list_dir {"path":".","depth":2} returned a 275-entry, 15,212-byte overview (tool result seq 1696). Its stored model preview is 4,364 bytes. Expanded .playwright-mcp logs dominate the head; a backup project dominates the tail. The preview contains neither the standalone gunmayhem-go/ entry nor gunmayhem-go/tmp.

The next batch includes list_dir("tmp"), which fails because that relative path is absent at the workspace root (seq 1697). The following calls use cmd dir /s /b tmp, list_dir("gunmayhem-go"), and list_dir("gunmayhem-go/tmp"). This is evidence of missing useful overview information and subsequent path exploration. It does not establish that all those calls would have been omitted with another ordering.

## Confirmed mechanism and fix

listTree already traverses breadth first to prevent one subtree consuming the entry budget before sibling roots. Before returning, however, it sorted all full paths globally. That moved every expanded log path ahead of other root entries and defeated their visibility in the head/tail preview.

Keep the existing traversal order in the result. os.ReadDir still sorts each individual directory; top-level entries precede their descendants, and traversal remains deterministic. No entry, field, instruction or filesystem operation is added. The full result has the same lines and byte count for a given traversal; only their order changes. Depth-1 listing, depth limits, shared entry cap, skipped subtree markers, sandbox resolution and symlink behavior are unchanged.

A root directory containing more entries than the preview budget can still require a narrowed listing or read_output. This fix specifically prevents an expanded child subtree from displacing already-discovered roots in the visible head.

## Verification

A filesystem-backed loop regression creates a root project between large log and backup trees, then calls list_dir at depth 2. It uses the real registry, result store and bounded model-facing output in both native and thin/hoisted modes.

Before the edit, both cases fail because the project root is absent from the next provider request. After the edit, both expose gunmayhem-go/ after one listing within the same output budget (about 22.3 KB full output, 4.3 KB model preview). The full result still includes the nested directory/depth marker and final log entries; files beyond the requested depth remain absent. Existing listing, entry-limit, ignored-root, cancellation and symlink tests also pass.

This is a deterministic evidence-availability result, not a real-model saved-turn or end-to-end latency measurement. The replay uses synthetic files, with no external model calls or persistent prompt additions.

Full go test ./... and go vet ./... pass. CLI and Windows GUI build, CLI --help smoke passes, and both executables are installed in the portable application directory with verified hashes and backups.

Artifacts: .tmp/directory-overview-preview-2026-09-27/{red,green,suite,vet,build-cli,build-gui,smoke,installed}.json and before-list_dir_tree.go.
