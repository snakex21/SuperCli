import { createClient } from "../net/client.js";

const GEO_HOST = "geo.internal";

export function geoClient() {
  return createClient({ host: GEO_HOST, retries: 5 });
}
