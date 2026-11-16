# logtail

Prints log lines that match filters.

    go run ./cmd/logtail --level warn samples/app-log.txt

Flags: `--level` (minimum level), `--now` (reference time, RFC 3339; default now).
