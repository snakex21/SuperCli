import { createClient } from "../net/client.js";

export function searchClient() {
  return createClient("search.internal", undefined, 750);
}
