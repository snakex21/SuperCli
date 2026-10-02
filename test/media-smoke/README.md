# Offline executable smoke

Requires the built Linux `supercli-web`, Python 3 and Node already installed.
Run from the repository root:

```
python test/media-smoke/http_smoke.py /absolute/path/to/supercli-web
```

This creates and removes its own temporary workspace, a fresh deterministic
pricing cache, a loopback model endpoint and a fixture-only stdio MCP process.
It never uses a real API key or captures the host screen. Ports 18788/18789 must
be free. It checks actual HTTP/SSE consent, native image/thumbnail and playable
video range serving, completion receipts and durable replay. This is not a
browser-rendering test or an AI generation quality test.

## Linux terminal smoke

Run `python test/media-smoke/tui_smoke.py /absolute/path/to/supercli` to exercise the actual executable in an 80×24 pseudo-terminal with echo mode and a fresh offline pricing cache. It checks startup/help, clears the input before a standalone `/quit`, verifies exit 0 and terminal-mode restoration, and reports observed exit latency. It does not invoke paid providers or claim inline terminal images.
