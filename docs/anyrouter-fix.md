# AnyRouter – wnioski z 2026-09-12 (do poprawy w SuperCli)

Kontekst: klucz AnyRouter `sk-d0Tf...TXRrx` działa, konto NIE jest zbannowane.
Problem to bramka (gate) AnyRouter + zły endpoint.

## Co ustalone (potwierdzone bare curlem przez kolegę)

1. Endpoint MUSI być Anthropic: `POST https://anyrouter.top/v1/messages`
   - `POST .../v1/chat/completions` (tryb OpenAI) jest martwy dla WSZYSTKICH modeli → 404 `当前 API 不支持所选模型`
2. Odblokowanie bramki 1M to JEDEN header:
   `anthropic-beta: context-1m-2025-08-07`
   - zwykła nazwa `claude-fable-5-1` + ten header = przechodzi
   - sufiks `[1m]` sam nic nie daje, data `2025-06-27` nie działa, UA/fingerprint Claude Code niepotrzebne
3. Działają tak: `claude-fable-5-1`, `claude-opus-4-7`, `claude-opus-4-5-20251101`, `claude-sonnet-4-5-20250929`, `claude-haiku-4-5-20251001`
   Nigdy: `gpt-*`, `gemini-*` (te tylko przez oficjalny Codex CLI)
4. Po bramce `503/429` = zapchany upstream, nie config → retry/backoff. Mają tak nawet prawdziwi Claude Code.
5. `1970-01-01` w tabeli tokenów = bug `Never expires` (epoch zero), token OK, quota ~997$.

## Stan w SuperCli (działa, ale do poprawy)

- `internal/llm/anthropic_beta.go:40` ma już stałą `context-1m-2025-08-07`
- `internal/llm/anthropic.go:192` robi `POST BaseURL + "/messages"`, base ma być `https://anyrouter.top/v1`
- `internal/llm/anthropic.go:227-232` robi auto-retry: pierwszy 400 o 1M → drugi request z betą. Działa, ale:
- BRAK `SUPERCLI_LLM_HEADERS` – nie da się wymusić custom headera z env/configu

## TODO do zrobienia

1. [ ] Dodać `SUPERCLI_LLM_HEADERS` / `headers` w `config.toml` dla providera `anthropic` (żeby dało się wysłać `anthropic-beta` od pierwszego requesta, bez tracenia jednego 400).
2. [ ] Dodać opcję `anthropic_1m_beta = true` albo `force_1m_header` żeby nie czekać na 400, tylko słać od razu (oszczędność 1 requesta na AnyRouter).
3. [ ] Lepsze błędy: rozróżnić w logu `404 zły endpoint (openai vs anthropic)` vs `400 gate 1M (brak bety)` vs `503 zapchany upstream (retry)`. Teraz wszystko wygląda jak "model nie działa".
4. [ ] Dodać preset/provider `anyrouter` w docs + przykład env:
   ```
   SUPERCLI_LLM_PROVIDER=anthropic
   SUPERCLI_LLM_BASE_URL=https://anyrouter.top/v1
   SUPERCLI_LLM_MODEL=claude-fable-5-1
   ```
5. [ ] Dopisać w `docs/configuration.md` ostrzeżenie: AnyRouter relay-only, tryb `openai` martwy, używać `anthropic`.
6. [ ] Sprawdzić Zen `TransportAnthropic` (`internal/app/provider_build.go:31-41`) czy też przekazuje betę – jak nie, to też dodać.

## Dlaczego warto (filozofia SuperCli)

- Klienty Node.js/TS (Codex CLI, Claude Code, opencode) biorą dużo tokenów samym sobą: wielki system prompt, definicje tooli, MCP, otoczka TS. Do tego ciężki runtime, wolny start.
- SuperCli (Go, jeden binarek) ma być szybkie i lekkie: pełna kontrola co wysyłamy, zero narzutu za cudzy framework.
- Dlatego nie kopiujemy całego Codexa, tylko minimalne 4 linijki co przepychają bramę: dobry endpoint + 2-3 nagłówki.

## Codex jest open-source – wykorzystać to

- Codex CLI jest open-source, więc widać dokładnie co dodali: jakie headery, jaki endpoint (`/v1/responses` vs `/v1/chat/completions`), jakie `User-Agent: codex_cli...`, jakie bety OpenAI.
- Lepsze AI ma to zrobić: przejrzeć upstream Codex, wyciągnąć minimalny fingerprint (endpoint + headery) potrzebny żeby AnyRouter przepuścił `gpt-*` i zaimplementować jako preset `provider=codex-anyrouter` bez ruszania reszty.
- Cel: GPT przez AnyRouter w lekkim SuperCli, bez kopiowania całego ciężaru Codex CLI.

Data: 2026-09-12
