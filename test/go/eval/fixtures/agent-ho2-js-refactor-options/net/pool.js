import { createClient } from "./client.js";

// createClientPool(size, host, port) returns `size` identical clients.
export function createClientPool(size, host, port) {
  const out = [];
  for (let i = 0; i < size; i++) {
    out.push(createClient(host, port));
  }
  return out;
}
