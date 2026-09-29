[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Přenosný AI agent pro programování napsaný v Go, s terminálovým rozhraním (TUI), desktopovým/webovým rozhraním (GUI) a dávkovým režimem, které sdílejí jeden engine.

<!-- readme-unit:status -->
Tento README popisuje verzi `1.0.0`. Přenosné balíčky se distribuují prostřednictvím [GitHub Releases](https://github.com/snakex21/SuperCli/releases); místní sestavení neznamená, že příslušná verze již byla zveřejněna.

<!-- readme-unit:h.screenshots -->
## Snímky obrazovky

<!-- readme-unit:screenshot.gui -->
![Webové rozhraní SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centrum akcí v terminálu SuperCli (TUI)](../screenshots/1.0.0/tui-actions-pl.jpg)

<!-- readme-unit:screenshots.more -->
Obrázek GUI je snímek obrazovky; obrázek TUI je vykreslením skutečného rozložení terminálového rozhraní aplikace. [Další snímky obrazovky](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Začínáme

<!-- readme-unit:start -->
Rozbalte balíček pro svůj operační systém a architekturu do složky s oprávněním k zápisu. Ve Windows spusťte `supercli.exe` pro terminál nebo `supercli-web.exe` pro GUI. Ponechte přiloženou složku `supercli-data/` vedle spustitelných souborů.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
V Linuxu nebo macOS použijte odpovídající spustitelný soubor z balíčku nebo sestavte aplikaci ze zdrojového kódu. Projekt vyberte pomocí `--home`; `--batch` použijte pro jeden dotaz bez TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Přenosná data

<!-- readme-unit:data -->
Nastavení, relace, paměť, přihlašovací údaje, mezipaměti, protokoly a zálohy jsou v `supercli-data/` vedle aplikace. Přenesením celé složky aplikace přenesete i je. Aplikace pro svůj stav nepoužívá `%APPDATA%`, `%LOCALAPPDATA%` ani registr Windows.

<!-- readme-unit:workspace -->
`--home` a `SUPERCLI_HOME` vybírají pracovní prostor bez přesunu dat aplikace. Projektová nastavení a pracovní artefakty používají `<project>/.supercli/`.

<!-- readme-unit:override -->
Umístění dat mění pouze výslovně zadané `--data-dir` nebo `SUPERCLI_DATA_DIR`. Pokud do složky aplikace nelze zapisovat, spuštění ohlásí chybu místo tichého použití složky uživatelského profilu.

<!-- readme-unit:legacy -->
Terminál může při prvním spuštění zkopírovat starší data z `~/.supercli` do prázdné přenosné složky a zachovat originál. Viz [uspořádání dat](../data-layout.md).

<!-- readme-unit:secrets -->
Přihlašovací údaje se přenášejí s přenosnou složkou. Uchovávejte ji v soukromí, zálohujte ji a nikdy nezapisujte API klíče ani autentizační soubory do veřejného repozitáře.

<!-- readme-unit:h.config -->
## Modely a konfigurace

<!-- readme-unit:providers -->
Poskytovatele nastavte v GUI nebo prostřednictvím TUI `/providers` a `/models`. Podporovaná připojení zahrnují endpointy kompatibilní s OpenAI, nativní Anthropic, ChatGPT/Codex OAuth, brány opencode a offline poskytovatele echo.

<!-- readme-unit:config -->
Globální nastavení je v `supercli-data/config.toml`; `<project>/.supercli/config.toml` je může přepsat. Proměnné prostředí a přepínače CLI mají přednost. Příklad místního endpointu:

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
Nahraďte ukázkový endpoint a model hodnotami svého serveru. Cloudové služby mohou vyžadovat přihlašovací údaje a účtovat používání. Schopnosti modelu určují vidění, nástroje, uvažování a limity kontextu. Viz [konfigurace](../configuration.md).

<!-- readme-unit:h.features -->
## Funkce

<!-- readme-unit:surface -->
GUI a TUI podporují stejných 27 jazyků rozhraní. Nabízejí streamované konverzace, obnovu relací, výběr projektu, správu modelů, přílohy a přehledy využití. TUI podporuje posouvání myší, hledání v konverzaci a sbalitelný výstup uvažování/nástrojů.

<!-- readme-unit:agent -->
Engine podporuje vyhledávání nástrojů, ověřené úpravy souborů, spouštění příkazů, historii relací, projektovou paměť, kompakci kontextu, cíle, delegování pracovním agentům, konzultace a volitelné modely pro návrhy.

<!-- readme-unit:tools -->
Nástroje zahrnují hledání v kódu, cílené čtení a opravy souborů, obrázky, archivy ZIP, dokumenty DOCX/XLSX/PDF a omezené provádění v kontextu. Dostupné nástroje závisejí na zvoleném profilu a modelu; aktuální katalog získáte vyhledáním nástrojů.

<!-- readme-unit:extensions -->
Volitelné balíčky MCP jsou v `supercli-data/mcp/` a spouštějí se při použití. Archiv vestavěných dovedností je v `supercli-data/skills/builtin-skills.zip`; jeho nepřítomnost nebrání běžnému spuštění. Rozšíření mohou potřebovat vlastní běhové prostředí nebo nainstalované hostitelské aplikace.

<!-- readme-unit:optional -->
Generování kandidátů Darwin, rady modelů a delegování pracovním agentům jsou volitelné postupy. Paralelní volání modelů může zvýšit spotřebu prostředků a náklady. Základní spustitelný soubor nevyžaduje Node, Python, Docker ani CGO.

<!-- readme-unit:h.controls -->
## Příkazy a ovládání

<!-- readme-unit:controls -->
Napište `/` pro paletu příkazů. Centrum akcí TUI otevřete pomocí `Tab` v prázdném vstupu nebo `Ctrl+K`; šipkami vybírejte akce a pomocí `Esc` se vraťte.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Příkaz | Akce |
| --- | --- |
| `/help`, `/doctor` | Zobrazit nápovědu a diagnostiku. |
| `/model`, `/models`, `/providers` | Vybrat modely a spravovat poskytovatele. |
| `/resume`, `/export` | Obnovit nebo exportovat konverzaci. |
| `/status`, `/cost`, `/compact` | Zkontrolovat využití nebo zkompaktovat kontext. |
| `/goal`, `/plan` | Spravovat cíle a režim plánování pouze pro čtení. |
| `/diff`, `/undo`, `/redo` | Zkontrolovat změny nebo vrátit/obnovit tah agenta. |
| `/darwin`, `/council` | Použít volitelné postupy s více modely. |
| `/update check\|download\|install` | Vyhledat, stáhnout nebo výslovně nainstalovat aktualizaci. |
| `/quit`, `/exit` | Ukončit aplikaci. |

<!-- readme-unit:keys -->
`Enter` odešle dotaz; `Ctrl+C` přeruší běh. `PgUp`/`PgDn` a kolečko myši posouvají; `Ctrl+F` hledá; `Shift+T` přepíná uvažování; `Shift+E` rozbaluje výstup nástrojů. Dotazy zadané během běhu se zařadí do fronty pro další bezpečný krok.

<!-- readme-unit:h.updates -->
## Aktualizace

<!-- readme-unit:update.check -->
Stabilní vydání na GitHubu vyhledejte na požádání na obrazovce About v GUI, pomocí TUI `/update check` nebo CLI `--check-update`. Kontrola nic neinstaluje.

<!-- readme-unit:update.download -->
Stažení připraví odpovídající přenosný balíček a ověří jeho SHA256 podle manifestu vydání. Stažení v GUI a `/update download` ponechají běžící instalaci na místě.

<!-- readme-unit:update.install -->
Tlačítkem instalace v GUI nebo `/update install` nainstalujte připravenou aktualizaci. CLI `--update` výslovně požaduje stažení a instalaci. Stávající `supercli-data/` se zachová a nahrazené spustitelné soubory se místně zálohují.

<!-- readme-unit:update.restart -->
Po instalaci restartujte ručně. Před instalací zavřete ostatní kopie; ve Windows mohou být spuštěné soubory zamčené. Pokud odpovídající balíček nebo ověřovací metadata nejsou dostupné, použijte ověřené ruční stažení vydání. Uchovávejte vlastní zálohu dat.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentace

<!-- readme-unit:docs.start -->
Začněte [indexem dokumentace](../README.md), [rychlým úvodem](../quickstart.md), [uspořádáním dat](../data-layout.md) a [konfigurací](../configuration.md).

<!-- readme-unit:docs.engine -->
Podrobnosti implementace najdete v dokumentech [architektura](../architecture.md), [delegování](../delegation.md), [výkon](../performance.md), [návrh GUI](../webgui.md) a [struktura projektu](../project-structure.md).

<!-- readme-unit:docs.extra -->
Průvodci funkcemi: [přenosné MCP](../portable-mcp.md), [vestavěné dovednosti](../builtin-skills.md) a [telemetrie](../telemetry.md). Sledování vývoje: [plán](../PLAN.md) a [roadmapa](../ROADMAP.md).

<!-- readme-unit:reference -->
[Předchozí úplný README](../readme-reference.md) je zachován jako historická reference; starší popisy funkcí mohou předcházet verzi `1.0.0`.

<!-- readme-unit:h.build -->
## Sestavení a ověření

<!-- readme-unit:build -->
Použijte verzi Go uvedenou v `go.mod`. Ve Windows `build.bat` sestaví TUI, `build_ui.bat` sestaví GUI a `run.bat` v případě potřeby aplikaci sestaví a spustí terminál.

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
Před vydáním spusťte testy Go. Kontrola dokumentace ověřuje všech 27 překladů, mapovaný obsah, nadpisy, odkazy, technické literály a shodné příklady kódu.

<!-- readme-unit:requirements -->
Běžné použití vyžaduje nakonfigurovaný endpoint modelu nebo účet. Git a ripgrep zlepšují práci s repozitáři; externí nástroje jsou volitelné, pokud je daný postup nevyžaduje. GUI navíc vyžaduje podporu webview/prohlížeče na dané platformě. K diagnostice instalace použijte `--doctor`.

<!-- readme-unit:h.license -->
## Licence

<!-- readme-unit:license -->
SuperCli používá [licenci MIT](../../LICENSE). Přibalené závislosti a obsah mají vlastní upozornění; viz [upozornění třetích stran](../../THIRD_PARTY_NOTICES.md) a [průvodce dovednostmi](../builtin-skills.md).
