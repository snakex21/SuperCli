# Architecture

Requests enter through `cmd/gateway`, pass authentication and throttling,
and are proxied upstream. Throttling historically used a fixed one-minute
window counter.
