# Kolory kart z folderu `stare`

Źródło porównania: `stare/SuperCli-main/internal/webgui`.

## Znaleziona różnica i poprawka

Paleta CSS, kolory zwykłych narzędzi, rzeczywistej aktywności delegacji oraz ramek kart odpowiadały źródłu. Różnica występowała w starszych transkryptach zawierających samą listę nazw narzędzi: nowy renderer nadawał im kolory według typu, podczas gdy oryginał miał neutralne etykiety.

`internal/webgui/assets/js/04-transcript.js` przywraca neutralny kolor tych etykiet. Tłumaczenia i zapisane liczby wywołań pozostają widoczne. Pełne, zapisane wywołania zachowują kolor i wynik jak aktywność na żywo.

Ograniczenie zgodności starej historii: jeżeli nie ma zapisanego wyniku pojedynczego narzędzia, status nadal jest oznaczony jako niepotwierdzony (`—`, szary). Stary renderer pokazywał w tym miejscu zielone `gotowe` dla całej listy, również bez danych o sukcesie poszczególnych wywołań. Nie przywrócono tego błędu. Treść briefu i tłumaczenia pozostają obsługiwane przez bieżący renderer.

## Weryfikacja

Rozszerzony `worker_original_layout_browser_test.go` porównuje 120 elementów geometrii i stylu z oryginalnymi źródłami. Obejmuje kolory tekstów, teł, ramek, kropek i statusów; odczyt, zapis, komendy i sieć; pracę, sukces, błąd oraz przywróconą historię. Sprawdzane motywy: dark/light/midnight. Profile fontów: systemowy i Georgia/Courier/skala 1.1.

- Przed poprawką test wykazał 12 rozbieżności kolorów nazw starszej historii dla każdego profilu.
- Po poprawce sześć kombinacji motywu i profilu przeszło.
- Testy historii i cyklu życia delegacji przeszły; ich oczekiwanie dla samych nazw dopasowano do wyglądu wskazanego oryginału.
- Natywny runner UI: 558/558.
- `go vet ./internal/webgui`, `go build ./...`, kompilacja obu aplikacji: przeszły.
- Pełnego `go test ./...` nie powtarzano dla tej zmiany wyglądu.

Logi, zrzuty i wyniki: `.tmp/worker-colors-2026-10-19/`. Podgląd w przeglądarce wykonano na syntetycznych danych i rzeczywistych ustawieniach wyglądu, bez wywołań modeli i zapisu do danych użytkownika.

Oba standardowe pliki wykonywalne i istniejące aliasy worker-ui podmieniono po wykonaniu kopii zapasowych; zgodność plików potwierdzono SHA-256. Tymczasowy serwer i karty podglądu zostały zamknięte.
