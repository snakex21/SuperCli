// createClient(host, port, timeoutMs, retries, tls) describes a connection to
// an internal service. Omitted (undefined) arguments take the defaults:
// port 80 (443 with tls), timeoutMs 5000 (0 = no timeout), retries 3, tls false.
export function createClient(host, port, timeoutMs, retries, tls) {
  if (!host) {
    throw new TypeError("createClient: host is required");
  }
  const secure = tls === true;
  const p = port === undefined ? (secure ? 443 : 80) : port;
  return Object.freeze({
    host,
    port: p,
    timeoutMs: timeoutMs === undefined ? 5000 : timeoutMs,
    retries: retries === undefined ? 3 : retries,
    tls: secure,
    url(path = "/") {
      return `${secure ? "https" : "http"}://${host}:${p}${path}`;
    },
  });
}
