# Repository preflight after a combined greeting

Reported GUI prompt: “cześć lubisz mnie?”. The shared lightweight router recognized “cześć” as an exact greeting and “lubisz” as a social prefix, but their combination fell through to the project route. That actually collected repository context; hiding the notice would not remove the work or tokens.

The shared GUI/TUI classifier now accepts a recognized short social message introduced by a complete greeting or acknowledgement, including whitespace/punctuation separation. Coordinator keywords are checked against the original prompt first. Unknown continuations such as “cześć kontynuuj” still retain project routing, and “projekcie” is recognized alongside “projekt”. No prompt instructions, provider calls, new mode or provider-specific path were added.

Regression coverage includes the exact screenshot input, punctuation, word-fragment boundaries, greetings before project requests, lazy one-time preflight collection and real GUI continuation from persisted messages. The screenshot greeting emits no preflight notice and reaches the provider once. A subsequent request to inspect project files receives the deferred repository context. Raw user transcript text remains free of internal context.

Full internal/agent and internal/webgui tests passed, together with go vet. Local Windows GUI and CLI binaries were rebuilt with the correction.
