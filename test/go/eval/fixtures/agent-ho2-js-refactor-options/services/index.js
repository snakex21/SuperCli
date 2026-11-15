import { authClient } from "./auth.js";
import { billingClient } from "./billing.js";
import { geoClient } from "./geo.js";
import { mailClient } from "./mail.js";
import { metricsClients } from "./metrics.js";
import { searchClient } from "./search.js";

export function allClients(authCfg) {
  return {
    auth: authClient(authCfg),
    billing: billingClient(),
    geo: geoClient(),
    mail: mailClient(),
    metrics: metricsClients(),
    search: searchClient(),
  };
}
