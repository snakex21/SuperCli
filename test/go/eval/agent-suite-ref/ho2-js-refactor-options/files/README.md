# svcmesh

Connection descriptors for our internal services.

## createClient

```js
import { createClient } from "./net/client.js";

const c = createClient({ host: "billing.internal", port: 8443, timeoutMs: 2000, retries: 0, tls: true });
```

Options: `host` (required), `port` (80, or 443 with tls),
`timeoutMs` (5000; 0 disables the timeout), `retries` (3), `tls` (false).
Omitted options take the defaults.

`createClientPool(size, host, port)` builds `size` identical clients.

Tests: `node --test`.
