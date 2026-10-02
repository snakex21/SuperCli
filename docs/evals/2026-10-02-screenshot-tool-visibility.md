# Requested desktop capture: expose the tool before the first model call

The user reported that the model refused a desktop screenshot, claiming that it was restricted to a terminal/sandbox. No capture tool call appeared. The capture implementation itself had passed native Windows checks, but `send_screenshot` was registered as a dormant capability. The regular thin profile did not carry its schema, so the model had to discover it through `tool_search` before it could act.

The shared agent loop now recognizes screen-capture references in the current request, selects coordinator capabilities without an extra navigator call, and includes the exact registered `send_screenshot` schema in that run's provider tool list. The tool description explicitly identifies native access to this computer's desktop and explains that no image-generation model is needed.

The schema is scoped to the current request. It does not activate a permanent tool, change discovered-tool state, add a tool absent from a restricted registry, or append a global instruction. Unrelated subsequent turns retain their previous schema set. The existing dispatcher can also invoke this exact one-turn tool while normal target validation stays in effect. Both GUI and TUI use this agent path.

## Verification

- Real-loop regression: the first provider request contains the screen-capture schema, the requested tool executes once, its snapshot exists and the following request produces the final reply. No discovery round is required. Both native calls and `invoke_tool` are covered.
- All combinations of thin/full and stable/dynamic tool settings expose the requested tool. The next unrelated request has no extra screenshot schema, and a restricted registry does not gain a missing capability.
- Live Qwen `qwen3.8-27b-uncensored`, local LM Studio, temperature 0, seed 31415, reasoning `none`: the exact user prompt was `cześć zrób zrzut pulpitu mi`.
- Control: suppress the screenshot definition on the provider boundary while keeping the same registered tool, model and base prompt. Qwen used `tool_search`, then `send_screenshot(source:screen)`, then replied: 3 provider requests.
- Fixed path: Qwen called `send_screenshot(source:screen)` on its first request and then replied: 2 provider requests, one successful capture, no errors.

Both live arms used an injected 1 × 1 synthetic PNG; no desktop pixels or real clipboard content were sent to the model. This tests model selection and execution of the tool, not screenshot interpretation accuracy or a guarantee that every model always chooses the same calls. Native Windows capture was separately tested in the preceding media work.

Private requests and results are kept in the ignored `.tmp/release-1.0.2/qwen-capture/` folder. Full Go tests and `go vet` passed after the change.
