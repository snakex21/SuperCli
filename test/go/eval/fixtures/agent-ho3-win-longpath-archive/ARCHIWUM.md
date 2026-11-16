# Archiwum umów - zasady

Każdy dokument z `do_archiwizacji/` trafia do:

    Archiwum umów/<pełna nazwa klienta>/<rok>/<numer> – <tytuł>.md

- `<pełna nazwa klienta>`: pole `nazwa` z `klienci.json` dla identyfikatora z pola `klient`, bez zmian
  (wielkość liter, spacje i polskie znaki zostają).
- `<rok>`, `<numer>`, `<tytuł>`: pola `rok`, `numer`, `tytuł` z nagłówka dokumentu.
- Między numerem a tytułem: spacja, półpauza (–, U+2013), spacja.
- Treść pliku nie może się zmienić ani o bajt (podpisy elektroniczne liczą skrót z całego pliku).

Przykład: dokument `XX-2022-0001` klienta `K-018` z roku 2022 o tytule `umowa testowa` trafia do
`Archiwum umów/Zielony Gaj Serwis sp. z o.o. oddział Kraków/2022/XX-2022-0001 – umowa testowa.md`.
