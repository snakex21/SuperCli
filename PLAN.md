# SuperCli — plan prac

Stan zweryfikowany względem kodu: **2026-07-16**.

Ten plik opisuje najbliższe działania. Pełny rejestr funkcji wdrożonych,
eksperymentów i dalszych pomysłów znajduje się w [ROADMAP.md](ROADMAP.md),
a wyniki pomiarów w [docs/performance.md](docs/performance.md). Nie powielamy
tu historii projektu, żeby plan nie starzał się po każdej większej zmianie.

## Teraz

- [ ] Rozbić największe pliki wewnątrz istniejących pakietów (`app/main.go`,
  `agent/loop.go`, `ui/tui/model.go`, `webgui/assets/app.js`) bez zmiany
  promptów, schematów narzędzi i formatów danych; po każdym etapie uruchomić
  replay, race i benchmark startu.
- [ ] Uruchomić pełną macierz `cmd/supercli-eval` na dużym Qwenie lokalnym,
  małym modelu lokalnym oraz modelach chmurowych; zapisać medianę i p95.
- [ ] Sprawdzić live routing navigatora na małym providerze.
- [ ] Zweryfikować streaming podsumowań kompakcji i raportów workerów.
- [ ] Przed wydaniem uruchamiać `cmd/supercli-perf` i porównywać cold start,
  czas pierwszego wyniku oraz peak RSS z poprzednim baseline'em.

## Następne, jeśli pomiary pokażą zysk

- [ ] Per-model prompty i mapy routera jako pliki w `.supercli/`, bez
  rekompilowania programu.
- [ ] Konsolidacja starych wpisów pamięci zamiast nieograniczonego wzrostu.
- [ ] UX Darwina: porównanie kandydatów, koszt przed startem i jawny wybór
  zwycięskiego diffu.
- [ ] Dalszy podział dużych plików wewnątrz istniejących pakietów, bez zmiany
  promptów, schematów narzędzi i formatów danych (kontynuować tylko tam, gdzie
  pomiar lub utrzymanie kodu uzasadnia kolejny podział).

## Zasady decyzji

1. Nie dodajemy stałego wywołania modelu dla funkcji czysto wizualnej.
2. Oszczędzona tura i stabilny KV-cache są ważniejsze niż mikrooptymalizacja
   kilku milisekund po stronie CLI.
3. Każda zmiana promptu lub protokołu przechodzi replay/eval przed uznaniem za
   lepszą.
4. Dane sesji, klucze, logi i binaria pozostają poza gitem.
5. `go.mod` i `go.sum` pozostają w katalogu głównym modułu — wymaga tego Go.
