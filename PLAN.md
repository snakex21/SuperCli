# SuperCli — Plan

## Zrobione

- [x] Schema fix — naprawiono generowanie JSON Schema dla tooli
- [x] Coordinator mode — domyślny tryb koordynatora
- [x] Workers (sync) — task odpala izolowane subagent loop
- [x] `send_message` — kontynuacja istniejącego workera
- [x] Async workers — task może działać w tle
- [x] Task notifications — worker po zakończeniu dostarcza `<task-notification>` do main chatu i TUI
- [x] Model-driven navigator — mały model wybiera mapę (chat/advisor/coordinator/clarify)
- [x] Równoległe tool calls — wiele `task` w jednej turze idzie równolegle

## Do zrobienia

### Wysoki priorytet

#### `/workers` — panel z aktywnymi workerami
- komenda TUI wyświetlająca status workerów: ID, agent, status, opis
- ma być czytelne i aktualne

#### `task_stop` — zatrzymywanie workera po ID
- narzędzie: `task_stop({"task_id":"worker-2"})`
- anuluje kontekst workera, zmienia status na `stopped`

#### `/context` — diagnostyka rozkładu tokenów
- pokazuje gdzie idą input tokeny:
  - navigator prompt
  - system prompt
  - tools
  - messages
  - worker notes
- tryb (chat/advisor/coordinator)

### Średni priorytet

#### Limity workerów
- `max_async_workers = 3`
- `max_writer_workers = 1`
- `worker_timeout = 5m`
- w config.toml i/lub zmienne środowiskowe

#### Scratchpad
- `.supercli/scratchpad/` — katalog współdzielony
- workerzy mogą czytać i pisać tam bez permisji
- coordinator może sprawdzać notatki bez wczytywania full outputu

#### Router map jako pliki
- `.supercli/router.md` — główna mapa nawigatora
- `.supercli/maps/code.md` — mapa do pracy z kodem
- `.supercli/maps/office.md` — mapa do dokumentów
- `.supercli/maps/git.md` — mapa do git
- Możliwość edycji bez kompilacji

### Niski priorytet

#### Prompt cache / stabilność requestu
- stabilizacja kolejności tools
- stabilizacja prompta
- unikanie zbędnych zmian system sections
- wsparcie dla providerów z cache
