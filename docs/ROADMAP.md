# SuperCli — Roadmapa

**Audyt produktowy:** 2026-06-11 · **odświeżenie utrzymania:** 2026-07-23.

SuperCli to portable, sandboxed agent CLI/Web w Go: jeden silnik
(`internal/agent` + `llm` + `tools` + `storage`), powierzchnie TUI
(`internal/ui/tui`), WebGUI (`internal/webgui`) i batch (`internal/app`).

**Jak czytać ten plik**

| Sekcja | Znaczenie |
|--------|-----------|
| Wdrożone | Jest w kodzie — nie re-implementować |
| Eksperymentalnie | Za knobem / profilem |
| Live-test | Mechanizm jest; brak domkniętego pomiaru na macierzy modeli |
| Backlog | Świadomie nie zrobione |

Pomiary i *dlaczego*: [performance.md](./performance.md),
[architecture.md](./architecture.md). Layout dysku:
[data-layout.md](./data-layout.md). Mapa źródeł:
[project-structure.md](./project-structure.md). Plan bliski:
[PLAN.md](./PLAN.md).

> **Uwaga po refaktorze (2026-07):** nazwy plików w `internal/` dostały
> prefiksy (`loop_*`, `tool_*`, `wire_*`, `cmd_*`…). Poniżej ścieżki wskazują
> **pakiet i koncept**; dokładna nazwa pliku może być `tool_repair.go` zamiast
> historycznego `toolcall_repair.go`. Źródło prawdy: kod + `go test ./...`.

### Ostatnio domknięte — utrzymanie kodu (2026-07-23)

- Monality rozbite w istniejących pakietach (`app` composition root,
  `agent` loop helpers, webgui assets/modules, LLM/storage/tools splits).
- Prefiksy plików + cienki root repo; docs: data-layout, project-structure.
- TUI/WebGUI nadal dzielą ten sam silnik (bez drugiego agenta).

### Wcześniej domknięte — TUI i odporność runtime (2026-07-16)

- **TUI bez kosztu dla modelu** — render ukończonej części rozmowy jest
  cache'owany, `Ctrl+F` wyszukuje bloki rozmowy, a pojedynczy wynik można
  zwinąć bez zmiany historii przekazywanej modelowi.
- **Wiadomość podczas pracy** — tekst wpisany w czasie inferencji trafia do
  ograniczonej kolejki i jest dołączany dopiero na bezpiecznej granicy między
  odpowiedzią a kolejnym krokiem; nie rozcina par tool-call/tool-result i nie
  uruchamia dodatkowego modelu w tle.
- **Obrona kompakcji** — podsumowanie jest przyjmowane tylko wtedy, gdy usuwa
  co najmniej 20% wycenianego prefiksu; stały narzut schematów narzędzi jest
  uwzględniany w progu i nie powoduje zapętlenia ponownej kompakcji.
- **Drzewa procesów na Windows** — MCP, LSP i długie sesje procesów korzystają
  z Job Object `KILL_ON_JOB_CLOSE`, więc procesy pomocnicze nie zostają po
  zamknięciu lub przerwaniu SuperCli.
- **Porządek repo** — odpowiedzialności katalogów opisuje
  `docs/project-structure.md`; modułowe `go.mod`/`go.sum` świadomie pozostają w
  korzeniu, a stan uruchomieniowy należy do `supercli-data/`.
- **Zgodność TUI i aplikacji** — TUI ma trwałą kolejkę zadań oraz bezpieczny i
  pełny eksport/import danych; WebGUI pokazuje wspólny rejestr workerów z
  możliwością zatrzymania i pełny raport Doctor. Test kontraktowy pilnuje
  obecności najważniejszych funkcji na obu powierzchniach. Operacje danych,
  diagnostyka i odświeżanie workerów nie wywołują modelu.

## Wdrożone (zweryfikowane w kodzie — NIE re-implementować)

Dawne "problemy z pudełka" i fale 1/3, plus większość dawnych TODO:

- **Onboarding wykrywa lokalne serwery** — probe `localhost:11434` (ollama)
  i `localhost:1234` (LM Studio) z listą modeli do wyboru
  (`internal/llm/providers`, `internal/ui/tui` onboard).
- **Doctor pinguje providerów** — checki osiągalności skonfigurowanego
  providera i lokalnych serwerów (`internal/system/doctor`).
- **Tool-call hardening** — naprawa uciętego/niedomkniętego JSON
  (domykanie stringów/nawiasów, trailing commas) + jedna runda re-promptu
  z błędem, gdy call jest nie do naprawy (`internal/agent` — `tool_repair`);
  sentinel-tagi zamiast JSON dla małych modeli (`tool_sentinel`).
- **Fala 3 w całości: `/workers`, `task_stop`, `/context`** —
  panel workerów + `/workers stop <id>` (`internal/app` — `cmd_workers`),
  narzędzie `task_stop` always-on (`tool_task_stop`),
  rozkład tokenów (`context_report`); inline status workerów w status bar.
- **Chudy protokół narzędzi (dawny TODO-priorytet) — DOWIEZIONY** —
  lekki katalog + `tool_search` zamiast pełnych schematów, sentinel-tagi,
  edycja z kotwicą na treści + numerem linii (`edit_line`), limity wyników
  narzędzi; wszystko live-mierzone (patrz [performance.md](./performance.md)).
- **Prompt cache / stabilność requestu** — `stable_toolset` domyślnie ON,
  demote system-wiadomości do ogona (`model_system_demote`), `cache_prompt`
  na lokalnych hostach, warm cache między sesjami (`slot_cache`,
  `meter_slotcache`).
- **Równoległe niezależne tool-calle** — wiele `task` w jednej turze
  równolegle (`task_parallel`).
- **Delegacja**: coordinator/workers, subagenci (`task` z depth-limit),
  orchestrator (okrojony rejestr), model-per-task (`task_model`),
  draft-verify (mały draftuje → sito → duży werdykt) — `docs/delegation.md`.
- **Limity i retencja workerów** — LRU-retencja zakończonych workerów ze
  snapshotem wyniku po evikcji + limit równoczesnych aktywnych; env-override
  `SUPERCLI_WORKER_RETENTION` / `SUPERCLI_MAX_ACTIVE_WORKERS`
  (`internal/agent/worker_registry.go`). Rozróżnienia read/write workerów
  w kodzie nie ma, więc `max_writer_workers` świadomie nie powstał.
- **Router multi-key / multi-provider (mechanizm)** — round-robin +
  safe-only failover (`internal/llm/router.go`). Otwarta zostaje wyłącznie
  POLITYKA ToS rotacji wielu kont — świadomy wybór użytkownika z odnotowanym
  ryzykiem, nie twarda blokada; oficjalny Codex auth
  (`internal/account/codexauth`) jest czysty implementacyjnie.
- **Kokpit HUD** — wskaźnik % limitu Codex 5h/tygodniowy
  (`internal/llm/codex_usage.go`), override cen per-provider/endpoint
  (`internal/account/credits/cost.go`, `providerRates` kluczowane
  `provider/model`), seed cen zna `gpt-5.5` / `gpt-5.4-mini` /
  `gpt-5.3-codex-spark`; spalone $ i tokeny w pasku statusu.
- **Higiena repo** — binarka/bazy .db/logi poza gitem; kod rozbity na
  `cmd/supercli`, `cmd/supercli-web` + pakiety domenowe.
- **Indeksy SQLite** — goal/memory/session mają indeksy
  (`internal/storage/*`).
- **Kompakcja kontekstu** — skalibrowany estymator, prune bez LLM,
  summary-fallback; telemetria faz per-step; `preflight_repo` ON;
  structured tool errors; caps wyników na granicy (`docs/performance.md`).
- **Adaptacyjna refleksja** — domyślnie bez sztywnej inferencji co 8
  kroków; self-review odpala się dopiero po powtarzających się błędach,
  identycznej partii tool-calli albo przed wyczerpaniem limitu kroków.
  Jawne `reflect_every = N` zachowuje tryb okresowy.
- **Uniwersalne grupowanie odczytów** — `read_many` pobiera do 12 zakresów
  w jednym tool-callu zarówno przez natywne API, jak i protokół sentinel;
  wynik ma globalny cap, częściowe błędy, stabilną kolejność. Partie jawnie
  oznaczonych narzędzi read-only wykonują się równolegle bez uruchamiania
  równoległych inferencji na lokalnej GPU.
- **Bezpośrednie proste narzędzia** — `invoke_tool` usuwa turę `tool_search`
  dla płaskich narzędzi read-only; jeden kontrakt działa przez natywne tools
  i sentinel, a mutacje/złożone schematy zostają na starej bezpiecznej ścieżce.
- **Deterministyczny advisor** — silne prefiksy pytań koncepcyjnych omijają
  inferencję navigatora, z pierwszeństwem słów projektowych/plikowych.
- **Projection dirty + retry** — projekcja nie zapisuje się nad niepełnym
  transcript boundary; po odzyskaniu appendów jest odbudowana z aktualnego
  widoku modelu i ponawiana, ze stanem widocznym w `/status`.
- **MCP klient** — konfiguracja serwerów MCP wróciła
  (`internal/tools/mcp`, `internal/app` — `cmd_mcp`), wyniki capowane na granicy.
- **Darwin (mechanizm)** — pool N agentów w izolowanych worktree, judge
  (LLM + heurystyka), opcjonalny auto-merge; `/darwin`.
- **Checkpointy per tura + `/undo` / `/redo`** — leniwa migawka przed pierwszą
  mutacją, wspólny zakres głównego agenta i workerów, konflikt-safe restore,
  trwała historia po restarcie oraz akcje w WebGUI. Osobny shadow-git nie
  dotyka brancha ani indeksu użytkownika; tury read-only nie inicjalizują repo.

## Wdrożone eksperymentalnie (za knobem)

- **Catalog hoist** — domyślnie aktywny dla profilu thin+stable po live A/B
  HP Z6/Qwen3.5-122B: 1743 → 237 ewaluowanych tokenów w dwóch ciepłych
  turach (−86,4%); thinking potwierdził widoczność narzędzia w obu położeniach,
  ale liczba powtórzeń nie jest porównywana z powodu znanej skłonności tego
  Qwena do losowych pętli tool-calli. Wyłączenie:
  `stable_toolset=false` albo `small_full_tools=true`.
- **Noop-gate** (`noop_gate`) — pomijanie identycznych batch-runów bez
  LLM; default OFF świadomie (zmienia semantykę odpowiedzi na pytania),
  opt-in dla idempotentnych pipeline'ów.

## Wymagające live-testu

- **Navigator na small-providerze** (fadc051) — klasyfikacja trasy na
  małym modelu; czeka na live-test.
- **Streaming podsumowań** — zweryfikować, czy compact/raporty workerów
  streamują zamiast czekać na pełną odpowiedź (pozycja z dawnego TODO
  wydajności; nie zweryfikowana w kodzie).

## Rzeczywisty backlog

### Fala 2 — pozostałość: per-model prompty i mapy jako pliki
- System prompt do plików (`.supercli/prompts/<rodzina>.md`), wzorzec
  opencode (`session/prompt/*.txt`).
- Mapy routera jako pliki: `.supercli/router.md`, `.supercli/maps/{code,office,git}.md`
  — edycja bez kompilacji.
- Gotowe gdy: zmiana zachowania na danym modelu nie wymaga rekompilacji.
  (Wybór formatu tool-calli native/sentinel per model już działa.)

### Scratchpad workerów
- `.supercli/scratchpad/` — katalog współdzielony; workerzy czytają/piszą
  bez permisji, coordinator sprawdza notatki bez wczytywania full outputu.

### Fala 5 — Darwin jako produkt
- Dopracować UX `/darwin`: prezentacja kandydatów (diff per worktree),
  koszt z góry, wybór judge, raport końcowy; rozważyć darwin-on-small-models.
- **Do doprecyzowania z użytkownikiem:** docelowy scenariusz (N prób tego
  samego promptu? różne modele? różne temperatury?), polityka auto-merge,
  budżet tokenów, czy judge ma być interaktywny.
- Gotowe gdy: `/darwin "zadanie"` pokazuje N diffów + werdykt sędziego
  i pozwala jednym klawiszem zaaplikować zwycięzcę.

### Wydajność / drobne
- **Ollama `keep_alive`** — ustawiać, żeby model nie wypadał z VRAM
  między requestami (nie znaleziono w kodzie).
- **Okresowa konsolidacja pamięci** — streszczanie starych wpisów memory
  zamiast nieograniczonego wzrostu.
- **Dalszy rozkład main.go** — `internal/app/main.go` ma nadal ~150k
  znaków; logika startowa do rozbicia.
- **models.dev jako źródło cen** obok pricepertoken.com i openrouter.ai
  (interfejs `Source` w `internal/account/pricing/pricing.go`).

### Kluczowe ficzery (otwarte)
- **Rekomendacja modelu pod VRAM** — przy wyborze modelu z Ollamy
  podpowiadać, co zmieści się w pamięci karty (inspiracja: odysseus
  "Cookbook" oparty o llmfit).
- **Opcjonalny autocommit po zadaniu agenta** — flaga/ustawienie.

### Szybkie winy (z backlogu 2026-06-13)
- Data + instrukcja "sprawdź aktualność bibliotek/wzorców przed użyciem,
  jest 2026" w system promptcie.
- Instrukcja researchu alternatyw przed wyborem biblioteki (web_search).
- Tryb portable jako OPCJA (`--portable` lub plik-marker: wszystko w
  `./supercli-data/` zamiast `~/.supercli`).

### Większe (z backlogu 2026-06-13)
- Selektywne czyszczenie kontekstu (UI pokazuje pełny transkript, model
  dostaje tylko potrzebne — context editing; częściowo pokryte przez
  prune/hide, patrz `docs/architecture.md`).
- Semantic search po kodzie zamiast całych plików (rozszerzenie
  istniejących embeddingów pamięci; inspiracja: semble).
- Stress-testing jako opt-in (`/test hard`: edge case'y, dane testowe,
  Playwright do wizualnej weryfikacji UI).
- Pełny fire-and-forget dekompozycji zadań (coordinator ma to robić bez
  dopytywania usera).
- Pattern learning — aktywne proponowanie domyślnych ustawień na bazie
  wzorców usera; konkretny wyzwalacz → undo z nauką (sekcja niżej).

**Odrzucone:**
- Scraping darmowych chatbotów przez przeglądarkę (ToS, kruchość) —
  zamiast tego tani/lokalny model.

### Wdrożone — undo per-wiadomość; otwarte powiązanie z Darwinem

Checkpoint całej tury, konflikt-safe undo/redo, WebGUI/TUI i zachowanie krótkiej
informacji o cofnięciu w kontekście modelu są wdrożone. Otwarte pozostaje
wykorzystanie tych samych preferencji przez sędziego Darwina.

1. **[DONE] Checkpointy zaczepione o turę czatu.** Leniwy snapshot powstaje
   przed pierwszą mutacją, a diff per-tura siedzi na shadow-gicie. Undo
   przywraca pliki; rozmowa nie jest destrukcyjnie ucinana — dostaje trwały
   marker, że wcześniejsza implementacja nie odpowiada już stanowi dysku.

2. **[PARTIAL] Undo jako sygnał uczenia (opcjonalny, NIEblokujący).** Po cofnięciu
   agent **może** zapytać "co było nie tak", ale **proponuje, nie żąda**.
   Dziś zachowujemy jedną strukturalną linię o cofnięciu; opcjonalne pytanie
   o powód i destylacja odpowiedzi pozostają otwarte.

3. **Routing lekcji.** Ogólna preferencja → **trwała pamięć**; specyficzna
   dla zadania → **tylko notatka sesyjna**. Jasny moment cross-session
   learningu: cofnąłeś → powiedziałeś czemu → trwała preferencja.

4. **Powiązanie z Darwinem.**
   - **Wspólna infrastruktura snapshotów** — Darwin już pracuje na
     izolowanych worktree; warstwę migawek zbudować RAZ dla undo
     (per-tura) i Darwina (per-worker).
   - **Lekcje preferencji karmią sędziego Darwina** — sędzia czytający
     preferencje wybiera "pod użytkownika".
   - **Dwa ustawienia tej samej gałki koszt/jakość** — undo (tanie,
     sekwencyjne) i Darwin (drogi, równoległy) uzupełniają się.

**Kolejność wdrożenia:** snapshoty → undo z nauką → dopiero potem spięcie
preferencji z sędzią Darwina. Undo **nie zależy** od Darwina.

### DONE — eval-harness dla agenta

`cmd/supercli-eval` uruchamia 10 izolowanych zadań fixture-driven z jasnym
pass/fail, macierzą modeli, oczekiwanymi/zabronionymi plikami, komendami
weryfikacji, timeoutem i raportem JSON. Zwykły runtime nie importuje harnessu,
więc sesje nie płacą za niego startem, RAM-em ani tokenami.

### TODO — live macierz modeli i regresje wydajności

- Uruchomić komplet 10 zadań `test/go/eval/suite.json` co najmniej na:
  Qwen3.5-122B z HP Z6 (thinking), małym modelu lokalnym oraz jednym małym
  i jednym mocnym modelu chmurowym.
- Dla każdego przebiegu zachować: pass/fail, czas, liczbę wywołań modelu i
  narzędzi, tokeny wejścia/cache/wyjścia oraz ewentualne użycie fallbacku.
- Porównywać medianę i p95 między wersjami CLI; pojedynczy szybszy przebieg
  nie może być podstawą optymalizacji promptu lub protokołu narzędzi.
- Dodać widoczne zdarzenie `primary -> fallback` do interfejsu i telemetrii,
  bez dodatkowego wywołania modelu.
- Uruchamiać `cmd/supercli-perf` przed wydaniem i ostrzegać przy regresji
  cold-start, first-output lub peak RSS; nie obciążać tym zwykłego startu CLI.
- Z dobrych rzeczywistych sesji tworzyć małe, zanonimizowane scenariusze eval:
  prompt, oczekiwane zmiany, test i budżet tur — bez archiwizowania pełnych,
  ciężkich wyników narzędzi.

## Inspiracje z konkurencyjnych CLI (gdzie szukać)

**opencode** (`opencode-dev/packages/opencode/src`):
- Per-model prompty: `session/prompt/*.txt` (osobne dla gemini/gpt/kimi/anthropic/beast) — gotowy wzorzec dla pozostałości Fali 2.
- Retry z poszanowaniem nagłówków `retry-after(-ms)` + backoff: `session/retry.ts`.
- Katalog modeli z models.dev (`provider/provider.ts`) — automatyczne ceny/limity.
- Kompaktacja/overflow: `session/compaction.ts`, `session/overflow.ts` — porównać z własnym auto-compact.

**codex** (`codex-main/codex-rs`):
- Pełny klient ollama z pull modeli i streamingiem postępu: `ollama/src/{client.rs,pull.rs}` — "pobierz model z poziomu CLI".
- Onboarding wieloekranowy: `tui/src/onboarding/` — krok "trust directory" warty skopiowania jako koncept bezpieczeństwa.
- `apply_patch` jako format edycji odporny na słabe modele (`apply-patch/`).

**claude-code** (`claude code orginalne/restored-src`, `package/`):
- Wzorzec system-remindera i sdk-tools.d.ts — odniesienie dla opisów tooli przyjaznych małym modelom.
- Skills/slash-komendy jako pliki md — SuperCli ma już skill_loader; warto ujednolicić.

**odysseus** (`odysseus-dev`):
- "Cookbook": skan sprzętu → rekomendacja modeli GGUF/FP8 z fit-score → pobranie i serwowanie jednym klikiem — najmocniejsza inspiracja dla rekomendacji pod VRAM.
- Compare/blind test modeli — pokrewne idei darwin.

**agent-go** (`agent-go-main`):
- Dokumenty projektowe PROMPT_CACHING_*.md — analiza pod prompt cache (wdrożony; referencja historyczna).
- Prosty REPL + todo-tracking jako referencja minimalizmu.
