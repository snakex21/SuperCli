import { createClient } from "../net/client.js";

// Payments must never be retried automatically.
export const billingClient = () => createClient("billing.internal", 8443, 2000, 0, true);
