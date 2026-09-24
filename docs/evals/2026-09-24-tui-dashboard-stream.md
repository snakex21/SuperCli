# TUI: panel, strumień pracy i polecenia Windows — 2026-09-24

## Zakres i przyczyny

- Nowy panel projektu i celu oraz trzy pola: tokeny, kontekst, cache. Węższe i niższe terminale używają układu tekstowego. Model i poziom myślenia pozostają w nagłówku.
- Poprzedni pasek czytał postęp celu z SQLite i kopiował/sumował historię statystyk podczas renderowania. Nowy odczytuje licznik tokenów oraz postęp aktualizowany przy zmianach celu. Obliczanie wysokości nie renderuje ponownie kart.
- Każda natywna delta rozumowania tworzyła osobny blok thinking. Teraz kolejne fragmenty tworzą jeden blok; odpowiedź, narzędzie i zakończenie zamykają go.
- Przed wywołaniem narzędzia dotychczasowa wypowiedź asystenta jest utrwalana we właściwym miejscu. Wyniki nie wyprzedzają już tekstu, który je poprzedzał.
- Wiersze rozmowy i narzędzi zawijają się według widocznej szerokości terminala, z uwzględnieniem ANSI. Zmiana rozmiaru unieważnia bufor gotowych wiadomości.
- Zmiany wysokości wejścia/panelu zachowują śledzenie końca. Ręczne przewijanie pozostaje respektowane; pasek podpowiada wtedy End, aby wrócić do bieżącej odpowiedzi. Wysłanie nowej wiadomości wraca do końca.
- Shift+E renderuje ponownie oryginalny wynik narzędzia. Wyniki procesów pokazują stdout/stderr zamiast jednoliniowego JSON; lista argumentów command jest widoczna w nagłówku.
- Jawne cmd.exe /c i /k otrzymują surową linię polecenia, ponieważ cmd nie stosuje reguł CommandLineToArgvW używanych przez Go os/exec. Inne programy zachowują dotychczasowe argumenty argv. Naprawa jest wspólna dla CLI i GUI.
- Automatyczne wzorce „no heuristic matched; defaulting to model” nie są już tworzone ani podawane z istniejącej pamięci do recall/injectora. Zachowano dane na dysku. Awaryjne przywołanie pamięci nie wstawia niepowiązanych wzorców błędów zamiast faktów o projekcie.
- Darwin pozostaje opcjonalnym porównywaniem wariantów, przydatnym tam, gdzie jakość uzasadnia dodatkowe próby. Naprawiono wybór aktualnego dostawcy po przełączeniu modelu; przełączenie lokalny/chmura aktualizuje też wykonywanie sekwencyjne/równoległe.

## GUI i czas do pierwszego fragmentu

Pierwszy fragment odpowiedzi lub rozumowania jest wysyłany od razu. Kolejne nadal korzystają z bufora 40 ms. Pierwsze malowanie tekstu w przeglądarce także omija timer; późniejsze aktualizacje Markdown pozostają grupowane.

Kontrolowany pomiar runStream z natychmiast odpowiadającym dostawcą testowym:
- nowa rozmowa: przygotowanie 56,57 ms;
- kolejna tura: 5,17 ms;
- odstęp dostawca → pierwszy emitowany fragment poniżej rozdzielczości zegara tego pomiaru.

To pomiar izolowanej ścieżki aplikacji, nie porównanie szybkości rzeczywistych modeli ani pełnego WebView. Nie odtworzono dokładnie zgłoszonej różnicy około 3 sekund. W dwóch ostatnich prostych turach Qwena zapisane backend_wait wynosiło około 1,90 s i 0,80 s, a przygotowanie kontekstu kilka milisekund. Nie ma podstaw, by obiecać usunięcie całego oczekiwania na dostawcę.

Bez dodatkowych instrukcji i wywołań modelu. Specjalna ścieżka OpenCode Zen pozostała bez zmian.

## Weryfikacja

- go test ./... — PASS.
- go vet ./... — PASS.
- node --test test/ui/i18n.test.cjs test/ui/stream-first-paint.test.cjs — 4/4 PASS.
- git diff --check — PASS.
- Testy cmd na Windows obejmują dir /b, type z nazwą zawierającą spację, osobne argumenty, if exist, cudzysłowy i zagnieżdżone cmd /c.
- Testy TUI: ciągłość myślenia, kolejność narzędzi, automatyczne i ręczne przewijanie, rozwijanie wyniku, zawijanie długich wierszy, przebudowa po zmianie rozmiaru oraz szerokości 120/90/80/42.
- Sprawdzono wizualnie ANSI wygenerowane przez rzeczywiste Model.View() w lokalnym podglądzie HTML, przy 120 i 72 kolumnach. Nie zastępuje to pełnej sesji w natywnym terminalu użytkownika.
- Końcowe uproszczenie liczenia wysokości panelu: ponowny test pakietu TUI — PASS.

Materiały pomiarowe, podgląd i kopie stanu przed zmianami znajdują się w .tmp/tui-dashboard-latency w folderze aplikacji.
