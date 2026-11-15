import { createClient } from "../net/client.js";

// cfg: { host, port?, timeoutMs?, retries?, tls? }
export function authClient(cfg) {
  return createClient(cfg.host, cfg.port, cfg.timeoutMs, cfg.retries, cfg.tls);
}
