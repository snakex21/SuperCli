# SuperCli — Plan

Stan zweryfikowany względem kodu 2026-07-12. Sekcje: wdrożone /
wdrożone eksperymentalnie (za knobem) / wymagające live-testu /
rzeczywisty backlog. Świeży stan wydajnościowy: `docs/performance.md`.

## Wdrożone (zweryfikowane w kodzie)

- [x] Schema fix — naprawiono generowanie JSON Schema dla tooli
- [x] Coordinator mode — domyślny tryb koordynatora
- [x] Workers (sync) — task odpala izolowane subagent loop
- [x] `send_message` — kontynuacja istniejącego workera (`internal/agent/send_message_tool.go`)
- [x] Async workers — task może działać w tle
- [x] Task notifications — worker po zakończeniu dostarcza `<task-notification>` do main chatu i TUI
- [x] Model-driven navigator — mały model wybiera mapę (chat/advisor/coordinator/clarify)
- [x] Równoległe tool calls — wiele `task` w jednej turze idzie równolegle
- [x] `/workers` — panel statusów workerów + `/workers stop <id>` (`internal/app/main_workers.go`, main.go ~1381)
- [x] `task_stop` — zatrzymywanie workera po ID, narzędzie always-on (`internal/agent/task_stop_tool.go`)
- [x] `/context` — diagnostyka rozkładu tokenów: system prompt, tools/katalog, messages, navigator (`internal/agent/context_report.go`)
- [x] Prompt cache / stabilność requestu — `stable_toolset` domyślnie ON, demote system-wiadomości do ogona, `cache_prompt` na lokalnych hostach, warm cache między sesjami (`slot_cache`); szczegóły i pomiary w `docs/performance.md`
- [x] Hardening tool calli — naprawa uciętego/niedomkniętego JSON + jedna runda re-promptu z błędem (`internal/agent/toolcall_repair.go`), sentinel-tagi dla małych modeli (`internal/agent/sentinel_toolcall.go`)
- [x] Grupowe odczyty lokalne/chmurowe — `read_many` (do 12 zakresów, częściowe błędy, globalny cap) działa przez natywne API i sentinel; partie wyłącznie read-only wykonują się równolegle z deterministyczną kolejnością wyników
- [x] `invoke_tool` — uniwersalny skrót dla prostych narzędzi read-only usuwa turę `tool_search`; natywne `args` i sentinel `arg.*`, pełna walidacja i replay 2-turn
- [x] Adaptacyjny navigator — silne pytania koncepcyjne idą deterministycznie do advisor, projekt/file nadal wygrywa
- [x] Projection dirty — retry aktualnego widoku po odzyskaniu appendów, bez zapisu starej projekcji pod nowym boundary
- [x] Limity workerów — retencja zakończonych workerów (LRU, snapshot wyniku po evikcji) + limit równoczesnych aktywnych workerów; env-override `SUPERCLI_WORKER_RETENTION` / `SUPERCLI_MAX_ACTIVE_WORKERS` (`internal/agent/worker_registry.go`); rozróżnienia read/write workerów nie ma, więc `max_writer_workers` świadomie nie istnieje

## Wdrożone eksperymentalnie (za knobem)

- Catalog hoist — katalog thin-tooli w stabilnym prefiksie promptu; `SUPERCLI_CATALOG_HOIST=1`, default OFF: live A/B LM Studio/Qwen3.5-9B dał tylko 0,83% mniej tokenów wejścia, a host nie raportował cached tokens
- Noop-gate (`noop_gate`) — pomijanie identycznych batch-runów; default OFF świadomie (zmienia semantykę odpowiedzi na pytania), opt-in dla idempotentnych pipeline'ów

## Wymagające live-testu

- Navigator na small-providerze (fadc051) — klasyfikacja trasy na małym modelu; czeka na live-test

## Rzeczywisty backlog

### Scratchpad
- `.supercli/scratchpad/` — katalog współdzielony
- workerzy mogą czytać i pisać tam bez permisji
- coordinator może sprawdzać notatki bez wczytywania full outputu

### Router map jako pliki
- `.supercli/router.md` — główna mapa nawigatora
- `.supercli/maps/code.md` — mapa do pracy z kodem
- `.supercli/maps/office.md` — mapa do dokumentów
- `.supercli/maps/git.md` — mapa do git
- Możliwość edycji bez kompilacji
