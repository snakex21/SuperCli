# Environment variables

The tile server is configured only through the environment.

| Variable | Default | Meaning |
|----------|---------|---------|
| `TILER_ADDR` | `:8700` | listen address |
| `TILER_MAX_ZOOM` | `20` | highest zoom level served (0-24) |
| `TILER_WORKERS` | `4` | render worker goroutines |
| `TILER_CACHE_DIR` | `cache` | on-disk tile cache directory |
| `TILER_CACHE_TTL_SECONDS` | `3600` | how long cached tiles stay fresh, in whole seconds |

Unset or empty variables use the default.
