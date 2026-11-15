import { createClient } from "../net/client.js";
import { createClientPool } from "../net/pool.js";

export function metricsClients() {
  const primary = createClient("metrics.internal", 9091, undefined, undefined, true);
  return [primary, ...createClientPool(2, "metrics-replica.internal", 9091)];
}
