# SuperCli — plan prac

Stan zweryfikowany względem kodu: **2026-07-23**.

Ten plik opisuje najbliższe działania. Pełny rejestr funkcji wdrożonych,
eksperymentów i dalszych pomysłów znajduje się w [ROADMAP.md](./ROADMAP.md),
a wyniki pomiarów w [performance.md](./performance.md). Nie powielamy
tu historii projektu, żeby plan nie starzał się po każdej większej zmianie.

## Zrobione niedawno (utrzymanie kodu)

- [x] Rozbić monality w istniejących pakietach (`app/main.go` ~3.4k→~0.8k,
  `agent/loop.go`, webgui JS, TUI, LLM, storage, tools) bez zmiany promptów,
  schematów narzędzi i formatów danych; prefiksy plików w `app` / `agent` /
  `webgui` / `tui` / `llm` (patrz [project-structure.md](./project-structure.md)).
- [x] Uporządkować root repo: plany/screenshoty w `docs/`, build baty + exe
  lokalnie w root; usunąć orphan `memory.db` / `sessions.db` / `.tmp-home` /
  archiwum one-shot splitterów.

## Teraz (produkt / pomiary)

- [ ] Uruchomić pełną macierz `cmd/supercli-eval` na dużym Qwenie lokalnym,
  małym modelu lokalnym oraz modelach chmurowych; zapisać medianę i p95.
- [ ] Sprawdzić live routing navigatora na małym providerze.
- [ ] Zweryfikować streaming podsumowań kompakcji i raportów workerów.
- [ ] Przed wydaniem uruchamiać `cmd/supercli-perf` i porównywać cold start,
  czas pierwszego wyniku oraz peak RSS z poprzednim baseline'em.

## Następne, jeśli pomiary pokażą zysk

- [ ] Per-model prompty i mapy routera jako pliki w data dir (nie mylić z
  legacy `.supercli/` w root — kanoniczne dane to `supercli-data/`), bez
  rekompilowania programu.
- [ ] Konsolidacja starych wpisów pamięci zamiast nieograniczonego wzrostu.
- [ ] UX Darwina: porównanie kandydatów, koszt przed startem i jawny wybór
  zwycięskiego diffu.
- [ ] Dalszy podział tylko tam, gdzie utrzymanie realnie boli (np. TUI
  `model.go` ~800 linii) — nie ciąć dla samego cięcia.

## Zasady decyzji

1. Nie dodajemy stałego wywołania modelu dla funkcji czysto wizualnej.
2. Oszczędzona tura i stabilny KV-cache są ważniejsze niż mikrooptymalizacja
   kilku milisekund po stronie CLI.
3. Każda zmiana promptu lub protokołu przechodzi replay/eval przed uznaniem za
   lepszą.
4. Dane sesji, klucze, logi i binaria pozostają poza gitem.
5. `go.mod` i `go.sum` pozostają w katalogu głównym modułu — wymaga tego Go.
