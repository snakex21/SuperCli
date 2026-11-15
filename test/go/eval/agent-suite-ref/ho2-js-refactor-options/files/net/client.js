// createClient({ host, port, timeoutMs, retries, tls }) describes a connection
// to an internal service. Omitted (undefined) options take the defaults:
// port 80 (443 with tls), timeoutMs 5000 (0 = no timeout), retries 3, tls false.
export function createClient({ host, port, timeoutMs = 5000, retries = 3, tls = false } = {}) {
  if (!host) {
    throw new TypeError("createClient: host is required");
  }
  const secure = tls === true;
  const p = port === undefined ? (secure ? 443 : 80) : port;
  return Object.freeze({
    host,
    port: p,
    timeoutMs,
    retries,
    tls: secure,
    url(path = "/") {
      return `${secure ? "https" : "http"}://${host}:${p}${path}`;
    },
  });
}
