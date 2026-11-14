# gateway architecture

- `internal/routes`: path table.
- `internal/proxy`: upstream forwarding.
- `internal/auth`: sessions.
- `internal/util`: misc helpers (to be split up, see issue #41).
