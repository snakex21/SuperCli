# polyglot orders

- `producer/` (Go): turns orders into OrderEvent JSON lines - `cd producer && go test ./...`
- `web/` (TypeScript, Node 24 runs .ts directly): `parseOrderEvent` - `node --test web/test/orderEvent.test.ts`
- `analytics/` (Python): `load_event` - `python -m unittest discover -s analytics/tests -t .`
- `schema/order_event.schema.json`: the contract; `python contract/check_contract.py` checks all three agree.
