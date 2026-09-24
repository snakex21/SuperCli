# TUI: kółko myszy i granica myślenia — 2026-09-24

- Włączono raportowanie myszy w uruchamianym terminalowym programie Bubble Tea.
- Kółko nad rozmową przewija wspólny viewport także podczas generowania. Ręczne przewijanie zatrzymuje śledzenie nowych fragmentów; powrót na dół lub End przywraca śledzenie.
- Kliknięcia, zdarzenia nad polem wpisywania i nad panelem oraz zdarzenia przy otwartym menu nie zmieniają rozmowy ani nie wysyłają szkicu.
- Myślenie ma przygaszoną kursywę i osobny nagłówek. Pierwszy widoczny tekst po myśleniu otrzymuje separator „Odpowiedź” / „Answer”. Separator działa również bez kolorów i kursywy.
- Po jawnym bloku reasoning zwykła odpowiedź nie jest ponownie klasyfikowana heurystycznie jako myślenie, np. dla początku „I think…”.
- Pusty zamknięty blok myślenia nie powoduje indeksowania pustej listy segmentów.
- Zmiany dotyczą prezentacji: bez dodatkowych instrukcji, tokenów i wywołań modelu.

Weryfikacja:
- go test ./internal/ui/tui ./internal/app — PASS.
- go vet ./internal/ui/tui ./internal/app — PASS.
- Testy zdarzeń myszy: przewijanie w spoczynku i podczas generowania, zachowanie szkicu, zatrzymanie i wznowienie śledzenia, ignorowanie kliknięć/panelu/menu.
- Testy renderera: rozdzielenie PL/EN, zwijanie myślenia, zachowanie prawdziwej odpowiedzi, brak przedwczesnego nagłówka przy streamingu.
- Podgląd ANSI z rzeczywistego Model.View() sprawdzony wizualnie przy 100 i 62 kolumnach, również bez kolorów. To weryfikacja renderera i symulowanych zdarzeń; nie test fizycznego kółka myszy w natywnym terminalu.
- Materiały: .tmp/tui-mouse-thinking w folderze aplikacji.
