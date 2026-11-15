# svcmesh

Connection descriptors for our internal services.

## createClient

```js
import { createClient } from "./net/client.js";

const c = createClient("billing.internal", 8443, 2000, 0, true);
```

Parameters, in order: `host` (required), `port` (80, or 443 with tls),
`timeoutMs` (5000; 0 disables the timeout), `retries` (3), `tls` (false).
Pass `undefined` to skip a parameter.

`createClientPool(size, host, port)` builds `size` identical clients.

Tests: `node --test`.
