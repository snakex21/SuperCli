import { createClient } from "../net/client.js";
import { createClientPool } from "../net/pool.js";

export function metricsClients() {
  const primary = createClient({ host: "metrics.internal", port: 9091, tls: true });
  return [primary, ...createClientPool(2, "metrics-replica.internal", 9091)];
}
