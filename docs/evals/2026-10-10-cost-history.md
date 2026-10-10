# Koszty według modelu i historyczne kursy

Panel statystyk zachowuje jedną kwotę kosztu. Kliknięcie otwiera szczegóły według dostawcy i modelu: tokeny, wywołania z raportowanym zużyciem tokenów i koszt. Można wybrać bieżącą rozmowę albo całą historię. Rozbicie wejścia, cache, wyjścia i myślenia jest dostępne przy liczbie tokenów.

Ustawienia zawierają jeden wybór waluty kosztów. Domyślną walutą pozostaje USD. Cennik edytowany ręcznie nadal podaje stawki w USD za milion tokenów; zmiana waluty prezentacji nie zmienia jednostki cennika.

## Trwałość wyceny

- Po wywołaniu zapisywane są liczniki, rzeczywisty dostawca/model oraz dostępna wtedy wycena w USD. Późniejsze zmiany cennika nie zmieniają zapisanej kwoty, także gdy wycena była nieznana albo model działał lokalnie.
- Cache i tokeny myślenia pozostają częściami wejścia i wyjścia. Nie są dodawane drugi raz do sumy tokenów.
- Dla innych walut zapisujemy przelicznik przypisany do dnia użycia oraz datę publikacji i źródło kursu. Każde wywołanie jest przeliczane według zapisanego dnia; suma nie korzysta z dzisiejszego kursu.
- Źródłem kursów jest tabela A NBP. Dla dnia bez publikacji stosowana jest ostatnia dostępna tabela z datą nie późniejszą niż dzień użycia. Kwoty są szacunkami kosztu API, a kursy są kursami średnimi, nie kosztem rozliczenia karty.
- Starsze rekordy są jednorazowo rekonstruowane z dostępnych cen i oznaczone w popupie. Nie można odtworzyć niezapisanej oryginalnej ceny. Brak kursu jest pokazywany jawnie; nie staje się zerowym kosztem.
- Oddzielny dziennik rozliczeń zachowuje liczniki i wyceny po usunięciu rozmowy. Nie przechowuje treści rozmów ani kluczy API. Wyeksportowana kopia danych obejmuje również zapisane kursy; import sprawdza je przed zastąpieniem danych.

## Ograniczenie dodatkowej pracy

Odczyt statystyk i otwarcie popupu korzystają wyłącznie z zapisów lokalnych. Pobieranie kursów następuje po płatnym użyciu przy wybranej walucie innej niż USD, zmianie waluty lub jawnym ponowieniu w popupie. Zapisane dni i dni właśnie pobierane są pomijane.

Interfejs współdzieli jedno oczekiwanie na zakończenie pobierania w danym cyklu. Po zakończeniu odświeża aktualny panel/popup raz. Nie używa timerów do odpytywania postępu ani automatycznego ponawiania błędów. Anulowanie oczekiwania przez przeglądarkę nie przerywa wspólnego pobierania.

Dane pozostają w przenośnym katalogu aplikacji: `sessions.db`, `currency-rates.db` i konfiguracja. Testy i staging również zapisują dane w katalogu repozytorium.

## Qwen 3.8 i myślenie

GUI uzupełnia zapisane identyfikatory modeli o natywne metadane lokalnego endpointu. Zapisana lista nazw modeli nie może pomijać informacji o sterowaniu myśleniem. Spóźniona odpowiedź dla poprzedniego modelu nie nadpisuje nowego wyboru.

Sprawdzenie lokalnego `qwen3.8-27b-uncensored` przez metadane LM Studio potwierdziło przełącznik `off/on`. Przy zapisanym `xhigh` odpowiedź GUI zachowała tę preferencję i zgłosiła efektywne myślenie `on`. Test nie wywoływał generowania odpowiedzi ani ładowania modelu.

## Weryfikacja

- `go test -count=1 -timeout=300s` dla 13 pakietów i punktów wejścia: PASS (12 z testami, CLI bez osobnych testów). Zakres obejmuje kursy, wyceny, bazę sesji, LLM/factory, konfigurację, statystyki, GUI, TUI, aplikację i pętlę agenta.
- `go vet` dla tego samego zakresu: PASS.
- Testy interfejsu Node: 284/284 PASS, w tym popup, ustawienia waluty, opóźnione odpowiedzi, współdzielone oczekiwanie na kursy i selektor myślenia.
- Metadata-only test lokalnego Qwena 3.8: PASS; efektywne `on` przy zachowanym zapisanym `xhigh`, bez generowania odpowiedzi.
- Sprawdzenie zbudowanego GUI w przeglądarce na osobnym katalogu testowym: popup z trzema modelami, szczegóły kursów i selektor Qwena widoczne. Zmiana PLN → USD → PLN odświeżyła kwotę przy zachowaniu wyceny; konsola bez błędów.
- Zbudowano i zainstalowano `supercli.exe` oraz `supercli-web.exe`. Sprawdzono SHA-256 instalacji i kopii poprzednich plików, oba `--help`, a także lokalny `--echo --batch` z zapisem sesji w osobnym przenośnym katalogu.
- Manifest źródeł sprawdzony przed i po buildzie. Logi, obraz testowego popupu, binaria poprzedniej wersji i potwierdzenie instalacji są w `.tmp/cost-history/`.

Aktualne SHA-256: CLI `6D1FCEAE8C5342D8A5B358E69EBB3FE203C6293A1CE1B27E415E11E20116981E`, GUI `26F92CF7D5817F921F7677557F23211749236E5F8D3C27252BFCD5B7111F1987`. Kopia poprzednich binariów: `.tmp/cost-history/backups/d188628f-0bb6-4e97-8774-01ec97e1a25d/`.

To zmiana ewidencji i prezentacji kosztów. Nie mierzy ani nie obiecuje skrócenia samego generowania lub kompaktowania przez Qwena. Działająca instancja GUI wymaga ponownego uruchomienia, żeby użyć nowego kodu.
