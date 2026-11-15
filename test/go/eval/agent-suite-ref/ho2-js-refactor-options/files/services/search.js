import { createClient } from "../net/client.js";

export function searchClient() {
  return createClient({ host: "search.internal", timeoutMs: 750 });
}
