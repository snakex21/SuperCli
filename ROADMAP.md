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
3. **Doctor nie sprawdza lokalnych providerów.** `internal/system/doctor` sprawdza binarkę, katalogi, sesje, config — ale nie pinguje ollama/LM Studio ani nie mówi "uruchom `ollama serve`".
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

## TODO: wydajność i kluczowe ficzery

### Wydajność
1. **Prompt cache / keep-alive** — stabilny prefiks promptu pod cache providera; dla Ollamy ustawiać `keep_alive`, żeby model nie był wyładowywany z VRAM między requestami.
2. **Krótszy system prompt + mniej tooli dla tieru small** — mniejszy kontekst = szybsze TTFT i mniej błędów tool-calli na małych modelach.
3. **Streaming wszędzie** — zweryfikować, czy navigator i podsumowania (compact, raporty workerów) streamują zamiast czekać na pełną odpowiedź.
4. **Równoległe niezależne tool-calle** — wykonywać współbieżnie calle bez zależności między sobą.
5. **Indeksy SQLite + okresowa konsolidacja pamięci** — indeksy na memory.db/sessions.db, cykliczne odchudzanie starych wpisów.

### Kluczowe ficzery
1. **/workers i /context** — widoczność koordynatora: panel statusów workerów i rozkład tokenów.
2. **Darwin — decyzje produktowe + UI** — rozstrzygnąć: N prób tego samego promptu vs różne modele, auto-merge vs ręczne zatwierdzanie; dopiero potem UI.
3. **Checkpointy plików + /undo** — migawka przed każdą edycją agenta, cofanie jednym poleceniem. Pełny spec (undo per-wiadomość, sygnał uczenia, spięcie z Darwinem) → sekcja "TODO — undo per-wiadomość + powiązanie z Darwinem".
4. **Rekomendacja modelu pod VRAM** — przy wyborze modelu z Ollamy podpowiadać, co zmieści się w pamięci karty.
5. **Konsolidacja pamięci** — streszczanie starych wpisów memory zamiast nieograniczonego wzrostu.
6. **Opcjonalny autocommit po zadaniu agenta** — flaga/ustawienie: commit zmian po udanym tasku.

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

## Backlog pomysłów (2026-06-13)

**Już mamy (dopracować):**
- Delegacja do subagentów z czystym kontekstem = coordinator/workers (brakuje `/workers`, `/context` — fala 3).
- Parallel sampling + judge = darwin (dopracować UX i decyzje produktowe).
- Pamięć między sesjami = system memory z fali 2.
- Pattern learning = globalne preferencje (dodać: aktywne proponowanie domyślnych ustawień na bazie wzorców usera). Konkretny wyzwalacz cross-session learning → undo z nauką (sekcja "TODO — undo per-wiadomość + powiązanie z Darwinem"): cofnąłeś → powiedziałeś czemu → trwała preferencja, zamiast magii jasny moment.
- Draft model = zalążek (draft-provider do podsumowań; docelowo: mały model pisze, duży weryfikuje, bridge przez wspólny JSON schema).

**Szybkie winy:**
- Data + instrukcja "sprawdź aktualność bibliotek/wzorców przed użyciem, jest 2026" w system promptcie.
- Instrukcja researchu alternatyw przed wyborem biblioteki (web_search).
- Tryb portable jako OPCJA (`--portable` lub plik-marker: wszystko w `./supercli-data/` zamiast `~/.supercli`; domyślnie bez zmian, bo pamięć globalna międzyprojektowa wymaga stałej lokalizacji).

**Większe:**
- Selektywne czyszczenie kontekstu (UI pokazuje pełny transkript, model dostaje tylko potrzebne — context editing).
- Semantic search po kodzie zamiast całych plików (rozszerzenie istniejących embeddingów pamięci; inspiracja: semble — MCP server do code search, wymagałby klienta MCP).
- Stress-testing jako opt-in (`/test hard`: edge case'y, dane testowe, Playwright do wizualnej weryfikacji UI).
- Powrót MCP klienta (context7 — live docs bibliotek, semble) jako świadoma decyzja.
- Pełny fire-and-forget dekompozycji zadań (coordinator ma to robić bez dopytywania usera).

**Odrzucone:**
- Scraping darmowych chatbotów przez przeglądarkę (ToS, kruchość) — zamiast tego tani/lokalny model.

## TODO — chudy nie-JSON-owy protokół narzędzi (priorytet)

Cel: ściąć input-tokeny wysyłane do modelu w każdej turze (kluczowe dla lokalnych modeli na słabszym prefillu oraz dla kosztów API). Spójne z tezą SuperCli: lekkość, szybkość, mało tokenów.

1. **Wykrywanie narzędzi — lekki katalog zamiast pełnych schematów.** W system promptcie tylko nazwa narzędzia + jedna linia "jak użyć" zamiast pełnych schematów JSON. Pełne schematy zostawić wyłącznie dla małego, stałego rdzenia (read/edit/bash...). Długi ogon narzędzi dociągany przez semantyczny retrieval z użyciem ISTNIEJĄCEGO modelu embeddingów (`internal/storage/memory/embed.go`) — tool RAG, bez dodatkowej tury modelu. Opcjonalnie wariant dwufazowy (model prosi o narzędzie → dostrzykujemy schemat) tylko dla bardzo rzadkich narzędzi.

2. **Wywołanie — tagi zamiast JSON.** Sentinel-znaczniki, których model nie wpisze przypadkiem (np. `«»`). Bez argumentów: `«time»`. Prosty argument: `«read: plik.go:0-100»`, `«grep: wzorzec»`. Złożone: blok z liniami `klucz: wartość` zamiast zagnieżdżonego JSON-a. Eliminuje całą klasę błędów uciętego/niezbalansowanego JSON-a (dziś łatane przez `toolcall_repair.go`). Per-model przełącznik: duże modele przez API zostają na natywnym JSON tool-callingu, lokalne/małe na tagach (powiązane z niezrobionym z Fali 2 "native tool-calls vs XML per model").

3. **Odczyt vs edycja — różny profil ryzyka.** Zasada: "read tani, write bezpieczny". Odczyt/grep zjechać do samych numerów linii (nic nie psuje). Edycja chroniona kotwicą na treści.

4. **Format edycji (złoty środek) — numer linii + kotwica na treści.** Łączy oba sygnały:
   - numer linii (`@200`) = podpowiedź GDZIE; rozstrzyga dwuznaczność, gdy ten sam fragment występuje wielokrotnie,
   - stara treść (`-`) = DOWÓD, że to właściwe miejsce; jeśli nie zgadza się verbatim, narzędzie PADA GŁOŚNO zamiast po cichu psuć kod (samo-walidacja),
   - nowa treść (`+`) = zmiana.
   Numer może się rozjechać o parę linii — narzędzie znajduje po treści w okolicy wskazanej linii. Echo'uje się tylko MINIMALNY unikalny fragment, nie cały blok. Neutralizuje obie pułapki edycji po liniach naraz: dryf numerów (treść waliduje) i niejednoznaczność (numer wskazuje). Edycje aplikować od najwyższego numeru linii w dół.

**Następny krok przed wdrożeniem (research, nie implementacja):** zmierzyć `/context` na świeżym buildzie (ile realnie żrą dziś schematy narzędzi) oraz rozpoznać obecną ścieżkę XML-tagów i obecne narzędzie edycji (czy kotwiczy na treści, czy na liniach) — żeby zdecydować, czy rozbudować to, co jest, czy stawiać obok.

## TODO — undo per-wiadomość + powiązanie z Darwinem

Rozszerza backlogowe "Checkpointy plików + /undo" i "Pattern learning". Cel: cofanie nie tylko ostatniej edycji, lecz całej tury czatu, z zachowaniem nauki z nieudanej próby. `/undo` istnieje, ale dziś to migawka per-edycja, nie per-wiadomość.

1. **Checkpointy zaczepione o turę czatu.** Każda wiadomość użytkownika = snapshot stanu plików. Cofnięcie do wiadomości N = restore plików do tego stanu **plus** ucięcie rozmowy za N (jak edycja wiadomości w czacie: stan plików i historia cofają się **razem**). Snapshotować **tylko zmienione pliki per tura** (diff / copy-on-write), nie całe drzewo — żeby było tanie. Może siedzieć na **shadow-gicie**.

2. **Undo jako sygnał uczenia (opcjonalny, NIEblokujący).** Po cofnięciu agent **może** zapytać "co było nie tak", ale **proponuje, nie żąda** (czasem cofasz po prostu żeby spróbować inaczej). Mechanizm godzący naukę z ucięciem rozmowy: przy cofnięciu wycinamy tokeny nieudanej próby, ale **zachowujemy z niej JEDNĄ linię nauki** (destylowaną). Czysty kontekst i zatrzymana wiedza naraz.

3. **Routing lekcji.** Ogólna preferencja ("wolę X", "nie ruszaj pliku Y") → **trwała pamięć** (ten sam system co deterministyczne fakty użytkownika). Specyficzna dla zadania → **tylko notatka sesyjna**. To konkretny wyzwalacz dla "cross-session pattern learning" z backlogu: zamiast magii jasny moment — cofnąłeś → powiedziałeś czemu → trwała preferencja.

4. **Powiązanie z Darwinem.**
   - **Wspólna infrastruktura snapshotów** — Darwin już pracuje na izolowanych worktree; warstwę migawek zbudować RAZ tak, by undo (per-tura) i Darwin (per-worker) z niej korzystały.
   - **Lekcje preferencji karmią sędziego Darwina** — dziś sędzia wybiera "najlepszą" próbę nie znając preferencji użytkownika, więc może wskazać wariant, który user by cofnął. Sędzia czytający preferencje wybiera "pod użytkownika" i z czasem trafniej.
   - **Dwa ustawienia tej samej gałki koszt/jakość** — undo (tanie, sekwencyjne) i Darwin (drogi, równoległy, N prób + sędzia) uzupełniają się, nie zastępują.

**Kolejność wdrożenia:** snapshoty → undo z nauką → dopiero potem spięcie preferencji z sędzią Darwina. Undo **nie zależy** od Darwina — budować pierwsze.

## TODO — router multi-key / multi-provider (round-robin + failover)

Spójne z tezą SuperCli o oszczędzaniu tokenów: "więcej mocy za mniej kasy" legalnie.

1. **Router rozkładający ruch na wiele kluczy API + wiele sesji (oficjalny Codex auth, `internal/account/codexauth`), które user LEGALNIE posiada.** Round-robin + failover świadomy limitów (na `429`/`5xx` przeskocz na kolejny klucz/sesję zamiast się wywalić) + routing "najpierw tani/lokalny, dopiero potem drogi". Wzorzec jak **LiteLLM**.

2. **Legalna kaskada realizująca cel "więcej mocy za mniej kasy".** Lokalny model jako podstawa (darmowy, bez limitów na sprzęcie użytkownika) → klucze API jako overflow z round-robinem/failoverem → oficjalne konto/konta przez **Codex auth** (`internal/account/codexauth`). Router obsługuje TĘ kaskadę.

**Rozdzielenie dwóch warstw — mechanizm vs polityka:**

- **MECHANIZM (czysty).** SuperCli ma już zaimplementowany **oficjalny** auth ChatGPT/Codex (`internal/account/codexauth`) zgodny z dokumentacją OpenAI (https://developers.openai.com/codex/auth) — sankcjonowany flow OAuth, logowanie kontem ChatGPT, **zero scrapingu / nieoficjalnego dostępu do sesji**. Dzięki temu obsługa wielu sesji jest technicznie czysta i łatwa: każde konto loguje się oficjalnie tym samym flow. Implementacyjnie nic nie łamie — argument "wymaga nieoficjalnego dostępu" tu **nie obowiązuje**.

- **POLITYKA (świadomy wybór użytkownika, nie twarda blokada).** Otwarte zostaje wyłącznie pytanie ToS: czy rotacja wielu **własnych, opłaconych** kont w celu **sumowania limitów** jest zgodna z ToS OpenAI. Usage-limity bywają per-konto, a mnożenie kont **bywa** traktowane jako obejście limitu — z ryzykiem bana konta. Decyzja należy do użytkownika; zapisujemy to jako **świadomy wybór z odnotowanym ryzykiem**, a nie jako "odrzucone / poza zakresem". Mechanizm jest gotowy do obsługi wielu sesji; user decyduje, ilu kont używa.

## TODO — samowystarczalny kokpit (HUD: tokeny + koszt + limity)

Cel: CLI pokazuje wszystko z poziomu narzędzia — ile tokenów poszło, ile kosztowało, ile zostało limitu — bez wychodzenia. Część już istnieje (patrz niżej), dokładamy brakujące kafelki.

**Stan zastany (z diagnozy 2026-06-14):** auto-odświeżany katalog cen z TTL 24h + offline-seed **JUŻ JEST** (`internal/account/pricing/pricing.go`, seed w `internal/account/credits/cost.go:30-82`); spalone $ w HUD **JUŻ JEST** (`internal/account/cost/cost.go:108-120` -> `internal/ui/tui/status.go:18`); liczenie tokeny×cena per-request/sesja/model **JUŻ JEST** (`credits.CostFor`, `cost.go:195-203`), karmione `usage` z odpowiedzi (`internal/llm/codex.go:246-253`). Token-HUD jako taki działa — dokładamy mu kafelki.

Do zrobienia:

1. **Wskaźnik % limitu Codex (5h rolling + tygodniowy).** Dane przychodzą jako NAGŁÓWKI HTTP odpowiedzi `/responses`, ale `internal/llm/codex.go` czyta tylko body/SSE i je wyrzuca. Nagłówki: 5h `x-codex-primary-used-percent` / `-window-minutes` / `-reset-at`, tygodniowy `x-codex-secondary-used-percent` / `-window-minutes` / `-reset-at`. Podpięcie: odczyt `resp.Header` w `doWithAuth` przy sukcesie (`internal/llm/codex.go:166`), snapshot (`used_percent`, `window_minutes`, `reset_at`, primary/secondary) przekazać do UI i wyrenderować w `internal/ui/tui/status.go` (analogicznie do kafelka `tok`, struct `StatusBar` `status.go:11-20`). WZORZEC 1:1 leży w repo: `codex-main/codex-rs/codex-api/src/rate_limits.rs`. **UWAGA — uczciwie:** nazwy nagłówków pochodzą z tego referencyjnego kodu, nie z odpowiedzi zaobserwowanej na żywo; PRZED wdrożeniem raz zdumpować realne nagłówki `/responses` (log `resp.Header` w `codex.go:166`), bo backend bywa w wariancie, gdzie limity przychodzą eventem SSE (`token_count`/`rate_limits`, por. `codex-rs` `RateLimitSnapshot`) zamiast nagłówkiem — wtedy parsować z SSE.

2. **Override cen per-proxy / per-endpoint.** Dziś `credits.RateFor` kluczuje cenę WYŁĄCZNIE po model-id i celowo odcina prefiks providera (`internal/account/credits/cost.go:165-188`, odcięcie `cost.go:170-172`) — więc ten sam model przez proxy o innym cenniku liczy się FAŁSZYWIE. Do zrobienia: rozszerzyć klucz stawek o providera/endpoint (mapa kluczowana `provider/model`, set przez `credits.SetFetchedRates` / `pricing.go:189-200`) i przekazywać providera do `CostFor` (callery `internal/account/cost/cost.go:32,53,80,109`); dodać pole na ręczne ceny per-endpoint w configu, wstrzykiwane z najwyższym priorytetem.

3. **(drobne) Uzupełnić seed cen o nowe modele.** Seed `modelRates` (`internal/account/credits/cost.go:33-46`) nie zna `gpt-5.5` / `gpt-5.4-mini` / `gpt-5.3-codex-spark` (z `codex_catalog.go:31`), więc bez sieci spadają na `default` 1.00/3.00. Dodać je do seedu.

4. **(opcjonalnie, pod auto-cennik) Dodać `models.dev` jako źródło cen** obok obecnych `pricepertoken.com` i `openrouter.ai` (interfejs `Source` `internal/account/pricing/pricing.go:57-60`, lista `pricing.go:67-71`).

## TODO — eval-harness dla agenta

Zestaw 10-15 nagranych zadań z jasnym pass/fail, odpalany jednym poleceniem, żeby przy dłubaniu w promptach/protokole narzędzi wychwytywać regresje jakości agenta ("czy nie zrobiłem go głupszym"). Nieoczywisty fundament — ratuje przed cichym dryfem jakości przy iteracji nad promptami.
