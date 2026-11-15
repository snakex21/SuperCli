// node scripts/healthcheck.js <host> [port]
import { createClient } from "../net/client.js";

const host = process.argv[2] || "localhost";
const port = process.argv[3] ? Number(process.argv[3]) : undefined;
const c = createClient({ host, port, timeoutMs: 1000, retries: 0 });
console.log(`checking ${c.url("/healthz")}`);
