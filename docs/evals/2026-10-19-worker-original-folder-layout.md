# Wygląd wywołań narzędzi według folderu `stare`

Źródło wskazane przez użytkownika: `stare/SuperCli-main/internal/webgui`.
Ten katalog zastępuje tag v1.0.14 jako odniesienie dla bieżącej poprawki.

## Zmiana

- `internal/webgui/assets/app.css`: przywrócone oryginalne szerokości kolumn nagłówka delegacji i wierszy aktywności oraz zachowanie statusów. Selektor nagłówka dotyczy bezpośredniego `summary`, aby nie zmieniał szczegółów narzędzi zagnieżdżonych w karcie.
- Wąskie karty dostają zmniejszane kolumny przez zapytanie kontenera; zwykły widok zachowuje metryki źródła. Nadal można rozwinąć wynik i argumenty narzędzia.
- Globalne style zwykłych tool calli, rozmiary fontów i odstępy treści już odpowiadały wskazanemu źródłu. Nie zastępowano całego arkusza ani ustawień użytkownika.
- Nie zmieniano promptów, poziomu myślenia, modeli ani sposobu zapisu aktywności.

## Sprawdzenie

Nowy `internal/webgui/worker_original_layout_browser_test.go` renderuje pełną powłokę aplikacji, zwykłe odczytanie pliku i delegację z trzema narzędziami. Porównuje 29 elementów: geometrię, fonty, odstępy, ramki i kolory, także przy zwinięciu całych kart. Dwa profile: systemowy oraz Georgia/Courier ze skalą 1.1. Treść testowa jest syntetyczna; wspólne tłumaczenia izolują wygląd od zmian nazewnictwa.

Wyniki:

- Porównanie z oryginalnym źródłem: oba profile przeszły, geometria w tolerancji 1 px, sprawdzane style zgodne.
- Testy aktywności, przywracania historii i stanu delegacji: przeszły.
- Pełna powłoka, delegacje przy szerokości 360/700/1250 px: 93 asercje, brak przepełnienia treści.
- Natywny runner UI: 558/558.
- `go vet ./internal/webgui`, `go build ./...` oraz kompilacja obu aplikacji: przeszły.
- Pełnego `go test ./...` w tej poprawce wizualnej nie powtarzano.

Uruchomienie porównania ze wskazanym źródłem wymaga ustawienia `SUPERCLI_WORKER_UI_SOURCE_DIR` na bezwzględną ścieżkę `stare/SuperCli-main/internal/webgui`. Bez niej test sprawdza sam bieżący widok, fonty i otwieranie wyników.

Zrzuty z przeglądarki i logi: `.tmp/worker-full-card-2026-10-19/` (`old-stare.jpg`, `current-stare.jpg`, `current-tool-detail.jpg`, `full-card-*.png`, `checks.jsonl`). Porównanie wizualne wykonano także w przeglądarce aplikacji przy tych samych ustawieniach Georgia/Courier/skala 1.1.

Oba standardowe pliki wykonywalne oraz istniejące aliasy worker-ui zostały przebudowane i podmienione; zgodność kopii sprawdzona SHA-256. Poprzednie pliki zachowano w katalogu testu previous-builds. Tymczasowe serwery i karty porównawcze zamknięto.
