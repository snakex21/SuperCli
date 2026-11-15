import { createClient } from "../net/client.js";

// The relay streams large batches: no timeout.
export function mailClient() {
  return createClient("smtp-relay.internal", 2525, 0);
}
