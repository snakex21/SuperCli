# Config schema 2

All files in `config/envs/` move from schema 1 to schema 2. The
loader (`app/config_loader.py`) must read schema 2 only and raise
`ValueError` (message containing `schema`) for anything else,
including old schema-1 files. The `Settings` dataclass and the
values it produces for each environment must stay exactly the same.

Schema 2 rules (key order does not matter):

- add `"schema": 2`;
- `timeout_ms` (int) becomes `timeout` (string): whole seconds are
  written as `"<n>s"` (5000 -> `"5s"`), anything else as
  `"<n>ms"` (1500 -> `"1500ms"`);
- `db_url` (`postgres://host[:port]/name`) becomes
  `"db": {"host": ..., "port": <int>, "name": ...}`; a missing port
  means 5432 and is written explicitly;
- `features` (comma separated string) becomes a JSON list: entries
  trimmed, empty entries dropped, duplicates removed (first
  occurrence wins), order kept;
- `log_level` becomes `"logging": {"level": <lower-cased value>}`;
  when `log_level` is missing write `"logging": {"level": "info"}`;
- every other key is kept unchanged.

Migrate the test fixture(s) under `tests/fixtures/` as well.
