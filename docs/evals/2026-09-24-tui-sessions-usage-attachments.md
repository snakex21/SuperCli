# TUI: sesje, zużycie, załączniki i Windows — 2026-09-24

## Zmiany

- Otwarcie sesji zastępuje widok pełną zapisaną historią zamiast dopisywać cztery spłaszczone wiadomości. Role, myślenie, Markdown, wywołania narzędzi i metadane załączników wracają do zwykłego renderera. Kontekst modelu nadal jest ładowany osobną, dotychczasową ścieżką. Nie ma wywołania modelu podczas otwierania.
- Błąd lub pusta sesja nie usuwa rozmowy z ekranu. Przed wczytaniem rozmowy z innego katalogu trzeba przełączyć projekt, aby narzędzia nie pracowały w niewłaściwym repozytorium.
- Dotychczasowa semantyka zapisu kontynuacji CLI pozostaje bez zmian: kolejne tury zapisuje sesja bieżącego uruchomienia, a źródłowa sesja pozostaje nienaruszona. Widok kosztów wyraźnie oddziela bieżące uruchomienie od danych zapisanej rozmowy.
- Tab → Zużycie lub /cost otwiera panel z tokenami wejścia, wyjścia, cache, wyceną i pomiarami wywołań. Identyfikator pochodzi z bieżącej sesji, nie z ostatniego rekordu bazy. Sumowanie wywołań uwzględnia także pomocnicze modele.
- GUI i TUI korzystają z jednej wyceny (internal/account/usagecost): darmowy, lokalny, abonament, stawka ręczna, szacunek lub brak ceny. Brak znanej stawki nie udaje kosztu zero.
- Raporty poleceń korzystają z formatowania Markdown. Odczyt wyniku lokalnego polecenia nie kasuje szkicu ani nie rozbraja równolegle uruchomionego agenta.
- Ctrl+O lub Tab → Załączniki otwiera przeglądarkę. Enter wybiera plik lub otwiera folder, lewo/prawo przełącza Pliki/Wybrane, Ctrl+L przyjmuje ścieżkę folderu, Delete usuwa wybór. Gotowe wraca do szkicu. Pliki wysyła dopiero kolejna wiadomość.
- Przygotowanie plików i obrazów współdzielone z GUI w internal/ui/attachments. Obrazy są wysyłane bezpośrednio; dokumenty otrzymują istniejące odwołania do czytników. Zachowane limity 8 plików, 32 MiB na plik, 64 MiB łącznie i obsługa sandboxa. Nieudane przygotowanie zachowuje wybór i przywraca tekst.
- Rozpoznawanie obrazu czyta najpierw 512 bajtów. Pozostała część dokumentu nie jest odczytywana tylko po to, aby wykluczyć obraz.
- CLI otrzymało wspólną z GUI ikonę PE oraz ikonę okna klasycznej konsoli. Ustawienie UTF-8 dotyczy tylko bieżącej konsoli i jest przywracane przy normalnym zakończeniu.
- Starsza konsola Windows automatycznie otrzymuje zamienniki nieobsługiwanych emoji. Nowoczesny terminal zachowuje emoji. Zmiana jest tylko w renderowaniu: historia, kopiowanie i dane modelu pozostają oryginalne. SUPERCLI_EMOJI=on wymusza oryginalne emoji; off wymusza zamienniki. Nie zmieniamy czcionek ani rejestru Windows.

## Sprawdzenie

- go test ./... — PASS.
- go vet ./... — PASS.
- Regresje: pełna historia, błędy odczytu, zachowanie szkicu, sumowanie wywołań pomocniczych, wysyłanie pikseli i dokumentów, błędny załącznik, wybór/usuwanie plików, emoji bez zmiany danych oraz szerokości 45/62/100/150 w PL i EN.
- Dotychczasowe testy GUI wyceny oraz załączników nadal korzystają z wrapperów nowej wspólnej implementacji i przechodzą.
- Kontrola wizualna: rzeczywisty wynik Go Model.View() przeniesiony z ANSI do HTML i obejrzany w przeglądarce. To kontrola renderowania, nie test na żywo w natywnym conhost.
- Bez dodatkowych wywołań modelu i bez zmian specjalnej ścieżki OpenCode Zen.

Artefakty testów i renderowania: .tmp/tui-sessions-usage-attachments/.
