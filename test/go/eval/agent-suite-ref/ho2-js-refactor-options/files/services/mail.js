import { createClient } from "../net/client.js";

// The relay streams large batches: no timeout.
export function mailClient() {
  return createClient({ host: "smtp-relay.internal", port: 2525, timeoutMs: 0 });
}
