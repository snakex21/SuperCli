import { createClient } from "../net/client.js";

// cfg: { host, port?, timeoutMs?, retries?, tls? }
export function authClient(cfg) {
  return createClient({ host: cfg.host, port: cfg.port, timeoutMs: cfg.timeoutMs, retries: cfg.retries, tls: cfg.tls });
}
