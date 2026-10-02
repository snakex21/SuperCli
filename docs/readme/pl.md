[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.3

<!-- readme-unit:intro -->
Przenośny agent AI do programowania napisany w Go, z interfejsem terminalowym (TUI), interfejsem desktopowym/webowym (GUI) i trybem wsadowym korzystającymi ze wspólnego silnika.

<!-- readme-unit:status -->
Ten README opisuje wersję `1.0.3`. Przenośne pakiety wydań są rozpowszechniane przez [GitHub Releases](https://github.com/snakex21/SuperCli/releases); lokalna kompilacja nie oznacza, że odpowiadające jej wydanie zostało już opublikowane.

<!-- readme-unit:h.screenshots -->
## Zrzuty ekranu

<!-- readme-unit:screenshot.gui -->
![Interfejs webowy SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centrum działań w terminalu SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Obraz GUI to zrzut ekranu; obraz TUI przedstawia wyrenderowany rzeczywisty układ interfejsu terminalowego aplikacji. [Więcej zrzutów](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Pierwsze kroki

<!-- readme-unit:start -->
Rozpakuj pakiet dla swojego systemu operacyjnego i architektury do folderu z prawem zapisu. W Windows uruchom `supercli.exe` dla terminala albo `supercli-web.exe` dla GUI. Dołączony folder `supercli-data/` pozostaw obok plików wykonywalnych.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
W Linux lub macOS użyj odpowiedniego pliku wykonywalnego z pakietu albo skompiluj kod źródłowy. Projekt wybierz przez `--home`; użyj `--batch` dla jednego polecenia bez TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Przenośne dane

<!-- readme-unit:data -->
Ustawienia, sesje, pamięć, dane uwierzytelniające, pamięć podręczna, logi i kopie zapasowe znajdują się w `supercli-data/` obok aplikacji. Przenieś cały folder aplikacji, aby zabrać je ze sobą. Aplikacja nie używa `%APPDATA%`, `%LOCALAPPDATA%` ani rejestru Windows do przechowywania swojego stanu.

<!-- readme-unit:workspace -->
`--home` i `SUPERCLI_HOME` wybierają obszar roboczy bez przenoszenia danych aplikacji. Nadpisania ustawień projektu i artefakty obszaru roboczego korzystają z `<project>/.supercli/`.

<!-- readme-unit:override -->
Lokalizację danych zmienia wyłącznie jawne `--data-dir` lub `SUPERCLI_DATA_DIR`. Jeśli folder aplikacji jest niezapisywalny, uruchomienie zgłasza błąd zamiast po cichu korzystać z katalogu profilu.

<!-- readme-unit:legacy -->
Przy pierwszym uruchomieniu terminal może skopiować starsze dane z `~/.supercli` do pustego przenośnego katalogu, zachowując oryginał. Zobacz [układ danych](../data-layout.md).

<!-- readme-unit:secrets -->
Dane uwierzytelniające przenoszą się razem z przenośnym folderem. Chroń jego prywatność, rób kopie zapasowe i nigdy nie zapisuj kluczy API ani plików uwierzytelniania w publicznym repozytorium.

<!-- readme-unit:h.config -->
## Modele i konfiguracja

<!-- readme-unit:providers -->
Skonfiguruj dostawców w ustawieniach GUI lub przez TUI `/providers` i `/models`. Obsługiwane połączenia obejmują endpointy zgodne z OpenAI, natywne Anthropic, ChatGPT/Codex OAuth, bramy opencode i dostawcę echo działającego offline.

<!-- readme-unit:config -->
Ustawienia globalne są w `supercli-data/config.toml`; `<project>/.supercli/config.toml` może je nadpisać. Zmienne środowiskowe i flagi CLI mają pierwszeństwo. Przykład lokalnego endpointu:

```toml
default_provider = "local"
default_model = "qwen"
language = "en"

[[providers]]
name = "local"
type = "openai"
base_url = "http://localhost:1234/v1"
api_key = "lm-studio"
model = "qwen"
```

<!-- readme-unit:config.note -->
Zastąp przykładowy endpoint i model wartościami swojego serwera. Usługi chmurowe mogą wymagać uwierzytelnienia i naliczać opłaty. Możliwości modelu określają obsługę obrazów, narzędzi, rozumowania i limity kontekstu. Zobacz [konfigurację](../configuration.md).

<!-- readme-unit:h.features -->
## Funkcje

<!-- readme-unit:surface -->
GUI i TUI obsługują te same 27 języków interfejsu. Zapewniają strumieniowane rozmowy, odzyskiwanie sesji, wybór projektów, zarządzanie modelami, załączniki i widoki użycia. TUI obsługuje przewijanie myszą, wyszukiwanie w rozmowie oraz zwijanie rozumowania i wyników narzędzi.

<!-- readme-unit:agent -->
Silnik obsługuje wyszukiwanie narzędzi, zweryfikowane edycje plików, wykonywanie poleceń, historię sesji, pamięć projektu, kompresję kontekstu, cele, delegowanie agentom roboczym, konsultacje i opcjonalne modele szkicujące.

<!-- readme-unit:tools -->
Narzędzia obejmują wyszukiwanie w kodzie, odczyt wybranych fragmentów i poprawki plików, obrazy, archiwa ZIP, dokumenty DOCX/XLSX/PDF oraz ograniczone wykonywanie w kontekście. Dostępne narzędzia zależą od wybranego profilu i modelu; użyj wyszukiwania narzędzi, aby poznać bieżący katalog.

<!-- readme-unit:extensions -->
Opcjonalne pakiety MCP znajdują się w `supercli-data/mcp/` i uruchamiają się przy użyciu. Archiwum wbudowanych umiejętności jest w `supercli-data/skills/builtin-skills.zip`; jego brak nie blokuje normalnego uruchomienia. Rozszerzenia mogą wymagać własnych środowisk wykonawczych lub zainstalowanych aplikacji gospodarza.

<!-- readme-unit:optional -->
Generowanie kandydatów Darwin, rady modeli i delegowanie agentom roboczym to opcjonalne sposoby pracy. Równoległe wywołania modeli mogą zwiększać zużycie zasobów i koszt. Główny plik wykonywalny nie wymaga Node, Python, Docker ani CGO.

<!-- readme-unit:h.controls -->
## Polecenia i obsługa

<!-- readme-unit:controls -->
Wpisz `/`, aby otworzyć paletę poleceń. Centrum akcji TUI otworzysz przez `Tab` przy pustym polu lub `Ctrl+K`; strzałkami wybieraj akcje, a `Esc` wraca do poprzedniego widoku.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Polecenie | Działanie |
| --- | --- |
| `/help`, `/doctor` | Pokaż pomoc i diagnostykę. |
| `/model`, `/models`, `/providers` | Wybierz modele i zarządzaj dostawcami. |
| `/resume`, `/export` | Wznów lub wyeksportuj rozmowę. |
| `/status`, `/cost`, `/compact` | Sprawdź użycie lub skompresuj kontekst. |
| `/goal`, `/plan` | Zarządzaj celami i trybem planowania tylko do odczytu. |
| `/diff`, `/undo`, `/redo` | Sprawdź zmiany albo cofnij/ponów turę agenta. |
| `/darwin`, `/council` | Korzystaj z opcjonalnych procesów z wieloma modelami. |
| `/update check\|download\|install` | Sprawdź, pobierz lub jawnie zainstaluj aktualizację. |
| `/quit`, `/exit` | Zamknij aplikację. |

<!-- readme-unit:keys -->
`Enter` wysyła polecenie; `Ctrl+C` przerywa. `PgUp`/`PgDn` i kółko myszy przewijają; `Ctrl+F` wyszukuje; `Shift+T` przełącza rozumowanie; `Shift+E` rozwija wyniki narzędzi. Polecenia wpisane podczas działania trafiają do kolejki na kolejny bezpieczny krok.

<!-- readme-unit:h.updates -->
## Aktualizacje

<!-- readme-unit:update.check -->
Sprawdzaj stabilne wydania GitHub na żądanie z ekranu About w GUI, przez TUI `/update check` lub CLI `--check-update`. Sprawdzenie niczego nie instaluje.

<!-- readme-unit:update.download -->
Pobranie przygotowuje odpowiedni przenośny pakiet i weryfikuje jego SHA256 z manifestem wydania. Pobranie w GUI i `/update download` pozostawiają uruchomioną instalację na miejscu.

<!-- readme-unit:update.install -->
Użyj przycisku instalacji w GUI lub `/update install`, aby zainstalować przygotowaną aktualizację. CLI `--update` jawnie żąda pobrania i instalacji. Istniejący `supercli-data/` jest zachowany, a zastępowane pliki wykonywalne otrzymują lokalne kopie zapasowe.

<!-- readme-unit:update.restart -->
Po instalacji uruchom aplikację ponownie ręcznie. Przed instalacją zamknij inne kopie; działające pliki wykonywalne mogą być zablokowane w Windows. Jeśli brakuje odpowiedniego pakietu lub metadanych weryfikacji, użyj zweryfikowanego ręcznego pobrania wydania. Zachowaj własną kopię zapasową danych.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentacja

<!-- readme-unit:docs.start -->
Zacznij od [indeksu dokumentacji](../README.md), [szybkiego startu](../quickstart.md), [układu danych](../data-layout.md) i [konfiguracji](../configuration.md).

<!-- readme-unit:docs.engine -->
Szczegóły implementacji znajdziesz w dokumentach [architektura](../architecture.md), [delegowanie](../delegation.md), [wydajność](../performance.md), [projekt GUI](../webgui.md) i [struktura projektu](../project-structure.md).

<!-- readme-unit:docs.extra -->
Przewodniki po funkcjach: [przenośne MCP](../portable-mcp.md), [wbudowane umiejętności](../builtin-skills.md) i [telemetria](../telemetry.md). Śledzenie rozwoju: [plan](../PLAN.md) i [mapa rozwoju](../ROADMAP.md).

<!-- readme-unit:reference -->
[Poprzedni pełny README](../readme-reference.md) pozostaje odniesieniem historycznym; starsze opisy funkcji mogą pochodzić sprzed wersji `1.0.0`.

<!-- readme-unit:h.build -->
## Budowanie i weryfikacja

<!-- readme-unit:build -->
Użyj wersji Go podanej w `go.mod`. W Windows `build.bat` buduje TUI, `build_ui.bat` buduje GUI, a `run.bat` buduje w razie potrzeby i uruchamia terminal.

[docs/releasing.md](../releasing.md)

```bash
go test ./...
go build -o supercli ./cmd/supercli
go build -o supercli-web ./cmd/supercli-web
node docs/readme/check.cjs
```

```powershell
go test ./...
go build -o supercli.exe ./cmd/supercli
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
node docs/readme/check.cjs
```

<!-- readme-unit:tests -->
Przed wydaniem uruchom testy Go. Kontrola dokumentacji sprawdza wszystkie 27 tłumaczeń, przyporządkowaną treść, nagłówki, linki, literały techniczne i identyczne przykłady kodu.

<!-- readme-unit:requirements -->
Normalne użycie wymaga skonfigurowanego endpointu modelu lub konta. Git i ripgrep usprawniają pracę z repozytoriami; narzędzia zewnętrzne są opcjonalne, chyba że wymaga ich dany proces. GUI wymaga dodatkowo obsługi webview/przeglądarki na swojej platformie. Użyj `--doctor` do diagnostyki instalacji.

<!-- readme-unit:h.license -->
## Licencja

<!-- readme-unit:license -->
SuperCli korzysta z [licencji MIT](../../LICENSE). Dołączone zależności i treści mają własne informacje licencyjne; zobacz [informacje o podmiotach trzecich](../../THIRD_PARTY_NOTICES.md) i [przewodnik po umiejętnościach](../builtin-skills.md).
