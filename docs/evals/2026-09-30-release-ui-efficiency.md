# 1.0.0 UI efficiency checks

The GUI reuses the settings response for composer draft recovery, removing a second serialized GET /api/settings. It skips reasoning metadata lookup for the empty-model sentinel. Tests verify retained drafts and actual model IDs.

Only the selected non-English GUI catalog is fetched. The 27 catalogs occupy 1,128,912 bytes in source; the immediate English catalog occupies 35,372 bytes, with a 55,833-byte Ukrainian catalog fetched only when selected. This measures catalog payload, not complete HTTP traffic or model latency.

TUI catalogs load once per selected language. Windows amd64 / Ryzen 5800X3D microbenchmark: cold one-language parse 2.39 ms and 223 KB; all 27 languages 36.97 ms and 6.07 MB; warm lookup 44.1 ns and zero allocations. These are catalog microbenchmarks, not end-to-end agent speed claims. Concurrent lazy access passed the race detector.

No interface translations are added to model messages. No automatic release checks were added to startup.
