[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Prenosný AI agent na programovanie napísaný v Go, s terminálovým rozhraním (TUI), desktopovým/webovým rozhraním (GUI) a dávkovým režimom zdieľajúcimi jeden engine.

<!-- readme-unit:status -->
Tento README opisuje verziu `1.0.0`. Prenosné balíky vydaní sa distribuujú cez [GitHub Releases](https://github.com/snakex21/SuperCli/releases); lokálne zostavenie neznamená, že jeho vydanie už bolo zverejnené.

<!-- readme-unit:h.start -->
## Začíname

<!-- readme-unit:start -->
Rozbaľte balík pre svoj operačný systém a architektúru do priečinka s právom zápisu. Vo Windows spustite `supercli.exe` pre terminál alebo `supercli-web.exe` pre GUI. Priložený priečinok `supercli-data/` ponechajte vedľa spustiteľných súborov.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
V Linuxe alebo macOS použite príslušný spustiteľný súbor z balíka alebo zostavte zo zdrojového kódu. Projekt vyberte cez `--home`; pre jednu požiadavku bez TUI použite `--batch`.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Prenosné údaje

<!-- readme-unit:data -->
Nastavenia, relácie, pamäť, prihlasovacie údaje, vyrovnávacie pamäte, záznamy a zálohy sú v `supercli-data/` vedľa aplikácie. Presuňte celý priečinok aplikácie, aby ste ich preniesli so sebou. Aplikácia na svoj stav nepoužíva `%APPDATA%`, `%LOCALAPPDATA%` ani register Windows.

<!-- readme-unit:workspace -->
`--home` a `SUPERCLI_HOME` vyberajú pracovný priestor bez presunu údajov aplikácie. Projektové prepisy nastavení a pracovné artefakty používajú `<project>/.supercli/`.

<!-- readme-unit:override -->
Umiestnenie údajov mení iba výslovné `--data-dir` alebo `SUPERCLI_DATA_DIR`. Ak do priečinka aplikácie nemožno zapisovať, spustenie ohlási chybu namiesto tichého použitia priečinka profilu.

<!-- readme-unit:legacy -->
Terminál môže pri prvom spustení skopírovať staršie údaje `~/.supercli` do prázdneho prenosného adresára a zachovať originál. Pozrite [usporiadanie údajov](../data-layout.md).

<!-- readme-unit:secrets -->
Prihlasovacie údaje cestujú s prenosným priečinkom. Chráňte jeho súkromie, zálohujte ho a nikdy nevkladajte API kľúče ani autentifikačné súbory do verejného repozitára.

<!-- readme-unit:h.config -->
## Modely a konfigurácia

<!-- readme-unit:providers -->
Poskytovateľov nastavte cez GUI alebo TUI `/providers` a `/models`. Podporované sú endpointy kompatibilné s OpenAI, natívny Anthropic, ChatGPT/Codex OAuth, brány opencode a offline poskytovateľ echo.

<!-- readme-unit:config -->
Globálne nastavenia sú v `supercli-data/config.toml`; `<project>/.supercli/config.toml` ich môže prepísať. Premenné prostredia a CLI prepínače majú prednosť. Príklad lokálneho endpointu:

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
Nahraďte ukážkový endpoint a model hodnotami svojho servera. Cloudové služby môžu vyžadovať prihlasovacie údaje a účtovať používanie. Schopnosti modelu určujú videnie, nástroje, uvažovanie a limity kontextu. Pozrite [konfiguráciu](../configuration.md).

<!-- readme-unit:h.features -->
## Funkcie

<!-- readme-unit:surface -->
GUI a TUI podporujú rovnakých 27 jazykov rozhrania. Ponúkajú streamované rozhovory, obnovu relácií, výber projektov, správu modelov, prílohy a prehľady využitia. TUI podporuje posúvanie myšou, vyhľadávanie v rozhovore a zbaliteľný výstup uvažovania/nástrojov.

<!-- readme-unit:agent -->
Engine podporuje objavovanie nástrojov, overené úpravy súborov, vykonávanie príkazov, históriu relácií, projektovú pamäť, kompakciu kontextu, ciele, delegovanie pracovným agentom, konzultácie a voliteľné modely návrhov.

<!-- readme-unit:tools -->
Nástroje pokrývajú vyhľadávanie kódu, cielené čítanie a opravy súborov, obrázky, archívy ZIP, dokumenty DOCX/XLSX/PDF a obmedzené vykonávanie v kontexte. Dostupné nástroje závisia od vybraného profilu a modelu; aktuálny katalóg získate objavovaním nástrojov.

<!-- readme-unit:extensions -->
Voliteľné MCP balíky sú v `supercli-data/mcp/` a spúšťajú sa pri použití. Archív vstavaných zručností je v `supercli-data/skills/builtin-skills.zip`; jeho neprítomnosť nebráni bežnému spusteniu. Rozšírenia môžu potrebovať vlastné behové prostredia alebo nainštalované hostiteľské aplikácie.

<!-- readme-unit:optional -->
Generovanie kandidátov Darwin, rady modelov a delegovanie pracovným agentom sú voliteľné postupy. Paralelné volania modelov môžu zvýšiť spotrebu prostriedkov a náklady. Hlavný spustiteľný súbor nevyžaduje Node, Python, Docker ani CGO.

<!-- readme-unit:h.controls -->
## Príkazy a ovládanie

<!-- readme-unit:controls -->
Napíšte `/` pre paletu príkazov. Centrum akcií TUI otvorte cez `Tab` pri prázdnom vstupe alebo `Ctrl+K`; akcie vyberajte šípkami a vráťte sa cez `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Príkaz | Akcia |
| --- | --- |
| `/help`, `/doctor` | Zobraziť pomoc a diagnostiku. |
| `/model`, `/models`, `/providers` | Vybrať modely a spravovať poskytovateľov. |
| `/resume`, `/export` | Obnoviť alebo exportovať rozhovor. |
| `/status`, `/cost`, `/compact` | Skontrolovať využitie alebo skompaktovať kontext. |
| `/goal`, `/plan` | Spravovať ciele a režim plánovania iba na čítanie. |
| `/diff`, `/undo`, `/redo` | Skontrolovať zmeny alebo vrátiť/zopakovať ťah agenta. |
| `/darwin`, `/council` | Použiť voliteľné postupy s viacerými modelmi. |
| `/update check\|download\|install` | Skontrolovať, stiahnuť alebo výslovne nainštalovať aktualizáciu. |
| `/quit`, `/exit` | Ukončiť aplikáciu. |

<!-- readme-unit:keys -->
`Enter` odošle požiadavku; `Ctrl+C` preruší. `PgUp`/`PgDn` a koliesko myši posúvajú; `Ctrl+F` vyhľadáva; `Shift+T` prepína uvažovanie; `Shift+E` rozbaľuje výstup nástrojov. Požiadavky zadané počas behu sa zaradia do frontu na ďalší bezpečný krok.

<!-- readme-unit:h.updates -->
## Aktualizácie

<!-- readme-unit:update.check -->
Stabilné vydania GitHub kontrolujte na požiadanie na obrazovke About v GUI, cez TUI `/update check` alebo CLI `--check-update`. Kontrola nič neinštaluje.

<!-- readme-unit:update.download -->
Stiahnutie pripraví príslušný prenosný balík a overí jeho SHA256 podľa manifestu vydania. Stiahnutie v GUI a `/update download` ponechajú bežiacu inštaláciu na mieste.

<!-- readme-unit:update.install -->
Pripravenú aktualizáciu nainštalujte tlačidlom GUI alebo `/update install`. CLI `--update` výslovne žiada stiahnutie a inštaláciu. Existujúci `supercli-data/` sa zachová a nahradené spustiteľné súbory sa lokálne zálohujú.

<!-- readme-unit:update.restart -->
Po inštalácii reštartujte ručne. Pred inštaláciou zatvorte ostatné kópie; bežiace spustiteľné súbory môžu byť vo Windows zamknuté. Ak príslušný balík alebo overovacie metadáta chýbajú, použite overené ručné stiahnutie vydania. Uchovávajte vlastnú zálohu údajov.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentácia

<!-- readme-unit:docs.start -->
Začnite [indexom dokumentácie](../README.md), [rýchlym štartom](../quickstart.md), [usporiadaním údajov](../data-layout.md) a [konfiguráciou](../configuration.md).

<!-- readme-unit:docs.engine -->
Podrobnosti implementácie: [architektúra](../architecture.md), [delegovanie](../delegation.md), [výkon](../performance.md), [návrh GUI](../webgui.md) a [štruktúra projektu](../project-structure.md).

<!-- readme-unit:docs.extra -->
Sprievodcovia funkciami: [prenosné MCP](../portable-mcp.md), [vstavané zručnosti](../builtin-skills.md) a [telemetria](../telemetry.md). Sledovanie vývoja: [plán](../PLAN.md) a [roadmapa](../ROADMAP.md).

<!-- readme-unit:reference -->
[Predchádzajúci úplný README](../readme-reference.md) je zachovaný ako historická referencia; staršie opisy funkcií môžu predchádzať verzii `1.0.0`.

<!-- readme-unit:h.build -->
## Zostavenie a overenie

<!-- readme-unit:build -->
Použite verziu Go uvedenú v `go.mod`. Vo Windows `build.bat` zostaví TUI, `build_ui.bat` GUI a `run.bat` zostaví podľa potreby a spustí terminál.

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
Pred vydaním spustite Go testy. Kontrola dokumentácie overuje všetkých 27 prekladov, priradený obsah, nadpisy, odkazy, technické literály a rovnaké príklady kódu.

<!-- readme-unit:requirements -->
Bežné používanie vyžaduje nakonfigurovaný endpoint modelu alebo účet. Git a ripgrep zlepšujú prácu s repozitármi; externé nástroje sú voliteľné, ak ich postup nevyžaduje. GUI navyše potrebuje podporu webview/prehliadača na platforme. Inštaláciu diagnostikujte cez `--doctor`.

<!-- readme-unit:h.license -->
## Licencia

<!-- readme-unit:license -->
SuperCli používa [licenciu MIT](../../LICENSE). Pribalené závislosti a obsah majú vlastné oznámenia; pozrite [oznámenia tretích strán](../../THIRD_PARTY_NOTICES.md) a [sprievodcu zručnosťami](../builtin-skills.md).
