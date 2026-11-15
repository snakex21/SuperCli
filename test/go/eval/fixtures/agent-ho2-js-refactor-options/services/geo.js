import { createClient } from "../net/client.js";

const GEO_HOST = "geo.internal";

export function geoClient() {
  return createClient(GEO_HOST, undefined, undefined, 5);
}
