import { createClient } from "../net/client.js";

// Payments must never be retried automatically.
export const billingClient = () => createClient({ host: "billing.internal", port: 8443, timeoutMs: 2000, retries: 0, tls: true });
