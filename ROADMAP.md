# SuperCli — Roadmapa (audyt 2026-06-11)

## (a) Stan obecny

SuperCli to agentowe CLI w Go (bubbletea TUI), repo: `SuperCli/SuperCli`. Spory, dojrzewający kod:

- **Pakiety** (`internal/`): `agent` (pętla, coordinator/workers, subagenci, navigator wybierający mapę chat/advisor/coordinator), `llm` (provider OpenAI-compatible + Codex Responses API, SSE, katalog modeli, probe z cache SQLite), `providers` (manager + 13 predefiniowanych endpointów, w tym ollama i lmstudio), `tier` (klasyfikacja small/big po cenie/nazwie/parametrach — adaptuje prompt i zestaw tooli), `tools` (~40 tooli: fs, search_code, edit_docx/xlsx, read_pdf/zip/image, web_search/fetch, outlook_mail, memory, goal, consult, file_ops z koszem), `darwin` (pula agentów w git worktree + LLM-as-judge, `/darwin`), `tui` (onboard, slash, autocomplete, theme, markdown), oraz `doctor`, `session`, `memory`, `compact`, `sandbox`, `planmode`, `mcp`.
- **Slash**: /help /model(s) /providers /status /cost /compact /clear /resume /export /diff /undo /plan /goal /reflect /sandbox /council /darwin /cmd /exit.
- **First-run**: jest minimalny wizard (`tui/onboard.go`, main.go:318) — 3 opcje: LM Studio (URL domyślny), OpenAI-compatible (URL+klucz), echo offline. Zapisuje config.toml.
- **Tolerancja na słabe modele**: jest realna baza — fallback XML `<tool_call>` w pętli (`agent/loop.go:982+`, parser niedomkniętych bloków), tier-aware prompt/toole, provider retry, auto-compact, kaskada context-window.
- **Darwin**: zaimplementowany (commity 3768965, 9dede4f, 98c1d83) — pool N agentów w izolowanych worktree, judge (LLM + heurystyka), opcjonalny auto-merge; nie jest jasne, jaki jest docelowy UX i kryteria sędziowania.
- **Kierunek z git log / PLAN.md**: coordinator+workers, navigator małym modelem, równoległe taski; TODO: /workers, task_stop, /context, scratchpad, mapy routera jako pliki, prompt cache.

## (b) Problemy posortowane po wpływie na "działa fajnie z pudełka"

1. **Onboarding nie wykrywa lokalnych serwerów.** Wizard ma 3 sztywne opcje; brak opcji Ollama (mimo że jest w PredefinedProviders), brak próbkowania `localhost:11434` / `localhost:1234` przy starcie, brak listy modeli z serwera i wyboru modelu — user po wizardzie nie wie, czy cokolwiek działa.
2. **Brak weryfikacji "hello" po onboardingu.** Wizard zapisuje config i wrzuca do chatu; pierwszy błąd (zły URL, serwer nie wstał, brak modelu) user widzi dopiero po pierwszym promptcie, jako surowy błąd HTTP.
3. **Doctor nie sprawdza lokalnych providerów.** `internal/doctor` sprawdza binarkę, katalogi, sesje, config — ale nie pinguje ollama/LM Studio ani nie mówi "uruchom `ollama serve`".
4. **Naprawa tool-calli jest częściowa.** Jest XML-fallback, ale brak: naprawy uciętego/niedomkniętego JSON w argumentach, retry z komunikatem "twój tool call był niepoprawny, popraw" (re-prompt), ani per-model promptów z przykładami formatu (opencode ma osobne prompty per rodzina modeli).
5. **main.go ma ~72k znaków** — monolit utrudnia rozwój onboardingu/CLI flags; logika first-run, providerów i pętli startowej splątana.
6. **Binarka, bazy .db i logi w repo** (supercli.exe 22 MB, memory.db, sessions.db) — śmieci w git, ryzyko konfliktów między maszynami.
7. **PLAN.md TODO niezrobione**: /workers, task_stop, /context — koordynator bez podglądu workerów jest "czarną skrzynką" w TUI.
8. **Darwin bez jasnego UX** — działa, ale brak dokumentacji kiedy go używać, ile kosztuje, jak wygląda wynik dla usera.

## (c) Fale pracy

### Fala 1 — First-run + niezawodność lokalnych modeli
Zakres:
- Autodetekcja przy starcie bez configu: probe `localhost:11434` (ollama `/api/tags`) i `localhost:1234/v1/models` (LM Studio); jeśli coś żyje — pokaż znalezione modele do wyboru jednym enterem (wzorzec: codex `codex-rs/ollama/client.rs`, opencode autodetekcja providerów).
- Po wyborze: test-request ("hello", 8 tokenów — probe już istnieje w `llm/probe.go`, użyć go w wizardzie) z czytelnym komunikatem sukces/porażka i podpowiedzią naprawy.
- Tool-call hardening dla small tier: (1) naprawa uciętego JSON (domknięcie nawiasów/cudzysłowów przy finish_reason=length), (2) gdy parse się nie uda — jedna runda re-promptu z błędem i przykładem poprawnego calla, (3) testy na korpusie typowych błędów qwen/llama.
- Doctor: checki "ollama reachable", "lmstudio reachable", "configured provider answers".
Gotowe gdy: świeży user z samym ollamą odpala supercli.exe, w ≤3 enterach rozmawia z modelem, a 7B model wykonuje read/edit/bash bez ręcznego poprawiania tool-calli w ≥90% prób testowych.

### Fala 2 — Per-model prompty i tier 2.0
Zakres: przenieść system prompt do plików (`.supercli/prompts/<rodzina>.md`), wzorzec opencode (`packages/opencode/src/session/prompt/{gemini,kimi,gpt,...}.txt`); mapy routera jako pliki (`.supercli/router.md`, `.supercli/maps/*` — już w PLAN.md); rozszerzyć tier o wykrywanie wsparcia native tool-calls vs XML (probe per model, cache już jest).
Gotowe gdy: zmiana zachowania na danym modelu nie wymaga rekompilacji, a wybór formatu tool-calli (native/XML) dzieje się automatycznie per model.

### Fala 3 — Widoczność koordynatora w TUI
Zakres: domknąć PLAN.md wysoki priorytet: `/workers` (panel statusów), `task_stop`, `/context` (rozkład tokenów); plus inline status workerów w pasku statusu (wzorzec: opencode TUI session status / codex tui).
Gotowe gdy: user w każdej chwili widzi co robią workerzy, może zabić workera i zobaczyć gdzie idą tokeny.

### Fala 4 — Higiena repo i refaktor main.go
Zakres: usunąć z gita supercli.exe / *.db / logs (rozszerzyć .gitignore), rozbić main.go na `cmd/supercli` + pakiety (startup, flags, wiring); CI z `go test ./...` i buildem na Windows.
Gotowe gdy: main.go < 500 linii, `git status` czysty po sesji, testy zielone w CI.

### Fala 5 — Darwin jako produkt
Zakres: dopracować UX `/darwin`: prezentacja kandydatów (diff per worktree), koszt z góry, wybór judge, raport końcowy; rozważyć darwin-on-small-models (kilka tanich prób + judge dużym modelem) jako killer-feature lokalnego stacku.
**Do doprecyzowania z użytkownikiem:** docelowy scenariusz (N prób tego samego promptu? różne modele? różne temperatury?), polityka auto-merge, budżet tokenów, czy judge ma być interaktywny (user wybiera zwycięzcę).
Gotowe gdy: `/darwin "zadanie"` pokazuje N diffów + werdykt sędziego i pozwala jednym klawiszem zaaplikować zwycięzcę.

### Fala 6 — Polish i wyróżniki
Zakres: prompt cache / stabilizacja requestów (PLAN.md), scratchpad workerów, eksport sesji, ewentualnie cookbook-light: rekomendacja modelu pod VRAM (inspiracja: odysseus "Cookbook" oparty o llmfit).
Gotowe gdy: powtórne requesty trafiają w cache providera, a /doctor potrafi zaproponować model pasujący do sprzętu.

## Inspiracje z konkurencyjnych CLI (gdzie szukać)

**opencode** (`opencode-dev/packages/opencode/src`):
- Per-model prompty: `session/prompt/*.txt` (osobne dla gemini/gpt/kimi/anthropic/beast) — gotowy wzorzec dla Fali 2.
- Retry z poszanowaniem nagłówków `retry-after(-ms)` + backoff: `session/retry.ts` — do przeniesienia do `llm` retry.
- Katalog modeli z models.dev (`provider/provider.ts`) — automatyczne ceny/limity zamiast ręcznego seedowania `capabilities_seed.json`.
- Kompaktacja/overflow: `session/compaction.ts`, `session/overflow.ts` — porównać z własnym auto-compact.

**codex** (`codex-main/codex-rs`):
- Pełny klient ollama z pull modeli i streamingiem postępu: `ollama/src/{client.rs,pull.rs}` — wzorzec Fali 1 (autodetekcja `/api/tags`, a nawet "pobierz model z poziomu CLI").
- Onboarding wieloekranowy: `tui/src/onboarding/{welcome.rs,auth.rs,trust_directory.rs}` — w tym krok "trust directory" (warty skopiowania jako koncept bezpieczeństwa).
- Doctor sprawdzający providera: `cli/src/doctor.rs`.
- `apply_patch` jako format edycji odporny na słabe modele (`apply-patch/`).

**claude-code** (`claude code orginalne/restored-src`, `package/`):
- Wzorzec system-remindera i sdk-tools.d.ts (schematy tooli) — odniesienie dla opisów tooli przyjaznych małym modelom.
- Skills/slash-komendy jako pliki md — SuperCli ma już skill_loader; warto ujednolicić.

**odysseus** (`odysseus-dev`):
- "Cookbook": skan sprzętu → rekomendacja modeli GGUF/FP8 z fit-score (oparty o llmfit) → pobranie i serwowanie jednym klikiem — najmocniejsza inspiracja dla lokalnego onboardingu (Fala 6).
- Compare/blind test modeli — pokrewne idei darwin (Fala 5).

**agent-go** (`agent-go-main`):
- Dokumenty projektowe PROMPT_CACHING_*.md — gotowa analiza pod Falę 6 (prompt cache).
- Prosty REPL + todo-tracking jako referencja minimalizmu.
