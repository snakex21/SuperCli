# Historia narzędzi delegacji dla człowieka

## Rozpoznanie

Zrzut użytkownika odpowiada zapisowi z 18 października. Ten wiersz nie zawierał `Origin.WorkerActivity`; dostępny był raport i zbiorcze nazwy narzędzi. W bazie nadal istniał wynik wskazany przez hostowy `stored tool output`: 65 817 bajtów, w tym 60 814 bajtów historycznych obserwacji narzędzi.

Diagnostyka rzeczywistej bazy była tylko do odczytu (`mode=ro`). Do testu powstała osobna lokalna kopia wskazanego wiersza i wyniku, bez edycji historii użytkownika i bez wywołań modeli.

## Poprawka

- DTO transkryptu udostępnia identyfikator dostępu do starych obserwacji, kiedy istnieje rozpoznany hostowy odnośnik lub obserwacje zapisano inline. Nie wczytuje dużych wyników podczas otwierania listy wiadomości.
- W starszej karcie delegacji można rozwinąć „Odczyt zapisanego wyniku”. Wynik pobierany jest raz po kliknięciu, wyświetlany jako tekst i zapamiętywany w tej karcie. Zamknięcie przerywa oczekujące żądanie; stare odpowiedzi nie nadpisują nowej karty.
- `/api/workers/observations` sprawdza workspace, sesję, sekwencję i fizyczny identyfikator wiadomości. Odczyt jest ograniczony do wyniku należącego do tej sesji; prefiks raportu oraz identyfikator workera muszą odpowiadać oryginalnemu zapisowi. API nie przyjmuje dowolnego handle od przeglądarki.
- Duże wyniki czytane są tylko na żądanie, z limitem 128 KiB obserwacji. Tekst nie jest interpretowany jako HTML ani jako dowód sukcesu poszczególnych wywołań.
- Aktualny osobny dziennik GUI pozostał w kanonicznym transkrypcie. Rozszerzony test wykonuje trzy prawdziwe kompakcje, zamyka i otwiera bazę oraz sprawdza zachowanie dziennika. Payload GUI jest nadal usuwany z projekcji/modelowych requestów.

Zmiana nie modyfikuje myślenia, promptów ani narzędzi dostępnych modelowi. Nie zwiększa liczby wywołań AI. Używa istniejących tłumaczeń i przenośnej bazy aplikacji.

## Regresja przewijania wykryta podczas pełnych testów

`container-type: inline-size`, dodane wcześniej przy odtwarzaniu wyglądu kart, psuło kotwicę przewijania w wirtualizowanym transkrypcie. Zastąpiono je regułą responsywną bez nowego korzenia containment. Drift przy doładowaniu starszej strony zmienił się z 4114,56/3107,84 px (krótka/długa rozmowa) na 0/0 px. Porównanie 120 elementów ze źródłem `stare` w sześciu kombinacjach motywu i fontów oraz testy szerokości 360/700/1250 px przeszły.

## Wyniki

- Odczyt kopii rzeczywistego starego zapisu: odzyskano 60 814 bajtów obserwacji.
- Test API, ograniczeń dostępu i odrzucania cudzych/starych identyfikatorów: przeszedł.
- Test w natywnej przeglądarce: jawne otwieranie, pojedyncze pobranie, ponowne wiązanie historii, zachowanie treści przy zwijaniu, bez wykonywania HTML: przeszedł.
- Trzy kompakcje i restart dla delegacji synchronicznej oraz asynchronicznej, bez payloadu GUI w kontekście AI: przeszły.
- Regresje aktywności, historii i cyklu życia delegacji: przeszły.
- Natywne testy UI: 558/558.
- `go vet` zmienianych pakietów i `go build ./...`: przeszły.

Pełne testy magazynu sesji przeszły. W pełnym pakiecie GUI po naprawie przewijania pozostała pojedyncza porażka `TestRunStreamQuestionPausesProgressTimeout` z watchdogiem 200 ms; ten test nie dotyczy zmienianej ścieżki. Jego powtórzenie bez zmiany kodu przeszło 10/10. Nie oznaczamy pełnego pakietu GUI jako zielonego. Końcowa kompilacja całego projektu i obu aplikacji przeszła. Szczegóły są w `.tmp/worker-human-history-2026-10-19/checks.jsonl`.

## Granice odzyskiwania

Stare obserwacje są historycznymi, ograniczonymi snapshotami. Fragmentów, których nigdy nie zapisano lub których cache już wygasł, nie da się odtworzyć. Nowy podgląd nie dorabia brakujących argumentów, wyników ani statusów. Istniejące limity aktualnego dziennika GUI (64 podglądy / 128 KiB) nie zostały w tej zmianie powiększone.

Zmienione źródła: `internal/webgui/transcript_worker_observations.go`, `data.go`, `data_transcript.go`, `http_router.go`, `assets/app.css`, `assets/js/04-transcript.js`, `assets/js/08-sessions.js`, `internal/storage/session/feature_tool_outputs.go`. Testy: `transcript_worker_observations_test.go`, `internal/agent/worker_ui_activity_test.go`.

Przebudowane supercli.exe i supercli-web.exe podmieniono w katalogu aplikacji. Poprzednie pliki zachowano w previous-builds, zgodność nowych kopii potwierdzono SHA-256. Tymczasową przeglądarkę i serwer podglądu zamknięto. human-history.jpg jest podglądem na syntetycznych danych z ustawieniami wyglądu użytkownika.
