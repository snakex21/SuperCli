[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
En bærbar AI-kodningsagent skrevet i Go, med en terminalgrænseflade (TUI), en skrivebords-/webgrænseflade (GUI) og batchtilstand, der deler én motor.

<!-- readme-unit:status -->
Denne README beskriver version `1.0.0`. Bærbare udgivelsespakker distribueres via [GitHub Releases](https://github.com/snakex21/SuperCli/releases); en lokal bygning betyder ikke, at dens udgivelse allerede er offentliggjort.

<!-- readme-unit:h.screenshots -->
## Skærmbilleder

<!-- readme-unit:screenshot.gui -->
![SuperClis webgrænseflade (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperClis handlingscenter i terminalen (TUI)](../screenshots/1.0.0/tui-actions-pl.jpg)

<!-- readme-unit:screenshots.more -->
GUI-billedet er et skærmbillede; TUI-billedet er en gengivelse af programmets faktiske terminallayout. [Flere skærmbilleder](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Kom i gang

<!-- readme-unit:start -->
Udpak pakken til dit operativsystem og din arkitektur i en skrivbar mappe. På Windows starter du `supercli.exe` til terminalen eller `supercli-web.exe` til GUI. Behold den medfølgende mappe `supercli-data/` ved siden af de eksekverbare filer.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
På Linux eller macOS bruger du den tilsvarende eksekverbare fil fra pakken eller bygger fra kildekoden. Vælg dit projekt med `--home`; brug `--batch` til én forespørgsel uden TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Bærbare data

<!-- readme-unit:data -->
Indstillinger, sessioner, hukommelse, legitimationsoplysninger, cache, logfiler og sikkerhedskopier ligger i `supercli-data/` ved siden af programmet. Flyt hele programmappen for at tage dem med. Programmet bruger ikke `%APPDATA%`, `%LOCALAPPDATA%` eller Windows-registreringsdatabasen til sin tilstand.

<!-- readme-unit:workspace -->
`--home` og `SUPERCLI_HOME` vælger arbejdsområdet uden at flytte programmets data. Projektets tilsidesættelser og arbejdsfiler bruger `<project>/.supercli/`.

<!-- readme-unit:override -->
Kun et udtrykkeligt `--data-dir` eller `SUPERCLI_DATA_DIR` ændrer dataplaceringen. Hvis programmappen ikke er skrivbar, melder opstarten en fejl i stedet for ubemærket at bruge en profilmappe.

<!-- readme-unit:legacy -->
Terminalen kan ved første start kopiere ældre data fra `~/.supercli` til en tom bærbar mappe og bevare originalen. Se [dataopbygning](../data-layout.md).

<!-- readme-unit:secrets -->
Legitimationsoplysninger følger den bærbare mappe. Hold mappen privat, tag sikkerhedskopier, og læg aldrig API-nøgler eller godkendelsesfiler i et offentligt repository.

<!-- readme-unit:h.config -->
## Modeller og konfiguration

<!-- readme-unit:providers -->
Konfigurer udbydere gennem GUI-indstillingerne eller TUI `/providers` og `/models`. Understøttede forbindelser omfatter OpenAI-kompatible endpoints, direkte Anthropic, ChatGPT/Codex OAuth, opencode-gateways og en offline echo-udbyder.

<!-- readme-unit:config -->
Globale indstillinger ligger i `supercli-data/config.toml`; `<project>/.supercli/config.toml` kan tilsidesætte dem. Miljøvariabler og CLI-flag har forrang. Eksempel på et lokalt endpoint:

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
Udskift eksemplets endpoint og model med værdierne fra din server. Cloudtjenester kan kræve legitimationsoplysninger og opkræve betaling for brug. Modellens egenskaber bestemmer billedforståelse, værktøjer, ræsonnering og kontekstgrænser. Se [konfiguration](../configuration.md).

<!-- readme-unit:h.features -->
## Funktioner

<!-- readme-unit:surface -->
GUI og TUI understøtter de samme 27 grænsefladesprog. De tilbyder streamede samtaler, gendannelse af sessioner, projektvalg, modelstyring, vedhæftninger og forbrugsoversigter. TUI understøtter rulning med musen, søgning i samtalen og sammenklappeligt output fra ræsonnering/værktøjer.

<!-- readme-unit:agent -->
Motoren understøtter værktøjssøgning, verificerede filændringer, kommandoudførelse, sessionshistorik, projekthukommelse, komprimering af kontekst, mål, delegering til arbejdsagenter, konsultation og valgfrie kladdemodeller.

<!-- readme-unit:tools -->
Værktøjerne dækker kodesøgning, målrettet læsning og rettelse af filer, billeder, ZIP-arkiver, DOCX/XLSX/PDF-dokumenter og afgrænset kontekstudførelse. Tilgængelige værktøjer afhænger af den valgte profil og model; brug værktøjssøgning til det aktuelle katalog.

<!-- readme-unit:extensions -->
Valgfrie MCP-pakker ligger i `supercli-data/mcp/` og starter, når de bruges. Arkivet med indbyggede færdigheder ligger i `supercli-data/skills/builtin-skills.zip`; mangler det, hindrer det ikke normal opstart. Udvidelser kan kræve egne kørselsmiljøer eller installerede værtsprogrammer.

<!-- readme-unit:optional -->
Darwin-kandidatgenerering, modelråd og delegering til arbejdsagenter er valgfrie arbejdsgange. Parallelle modelkald kan øge ressourceforbrug og omkostninger. Kerneprogrammet kræver ikke Node, Python, Docker eller CGO.

<!-- readme-unit:h.controls -->
## Kommandoer og betjening

<!-- readme-unit:controls -->
Skriv `/` for kommandopaletten. Åbn TUI-handlingscentret med `Tab` i et tomt inputfelt eller `Ctrl+K`; brug piletasterne til at vælge handlinger og `Esc` til at vende tilbage.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Kommando | Handling |
| --- | --- |
| `/help`, `/doctor` | Vis hjælp og diagnostik. |
| `/model`, `/models`, `/providers` | Vælg modeller og administrer udbydere. |
| `/resume`, `/export` | Genoptag eller eksporter en samtale. |
| `/status`, `/cost`, `/compact` | Undersøg forbrug eller komprimer kontekst. |
| `/goal`, `/plan` | Administrer mål og skrivebeskyttet plantilstand. |
| `/diff`, `/undo`, `/redo` | Undersøg ændringer eller fortryd/gentag en agenttur. |
| `/darwin`, `/council` | Brug valgfrie arbejdsgange med flere modeller. |
| `/update check\|download\|install` | Søg efter, hent eller installer udtrykkeligt en opdatering. |
| `/quit`, `/exit` | Afslut programmet. |

<!-- readme-unit:keys -->
`Enter` sender en forespørgsel; `Ctrl+C` afbryder. `PgUp`/`PgDn` og musehjulet ruller; `Ctrl+F` søger; `Shift+T` slår ræsonnering til/fra; `Shift+E` udvider værktøjsoutput. Forespørgsler indtastet under en kørsel sættes i kø til et sikkert næste trin.

<!-- readme-unit:h.updates -->
## Opdateringer

<!-- readme-unit:update.check -->
Søg efter stabile GitHub-udgivelser efter behov fra GUI-skærmen About, TUI `/update check` eller CLI `--check-update`. Kontrollen installerer ikke noget.

<!-- readme-unit:update.download -->
Download klargør den passende bærbare pakke og verificerer dens SHA256 mod udgivelsesmanifestet. Download i GUI og `/update download` lader den kørende installation blive på plads.

<!-- readme-unit:update.install -->
Brug installationsknappen i GUI eller `/update install` til at installere den klargjorte opdatering. CLI `--update` anmoder udtrykkeligt om download og installation. Eksisterende `supercli-data/` bevares, og erstattede eksekverbare filer sikkerhedskopieres lokalt.

<!-- readme-unit:update.restart -->
Genstart manuelt efter installationen. Luk andre kopier før installation; kørende eksekverbare filer kan være låst på Windows. Hvis en passende pakke eller verificeringsmetadata ikke findes, skal du bruge en verificeret manuel download af udgivelsen. Behold din egen sikkerhedskopi af data.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentation

<!-- readme-unit:docs.start -->
Begynd med [dokumentationsindekset](../README.md), [hurtigstart](../quickstart.md), [dataopbygning](../data-layout.md) og [konfiguration](../configuration.md).

<!-- readme-unit:docs.engine -->
Læs [arkitektur](../architecture.md), [delegering](../delegation.md), [ydeevne](../performance.md), [GUI-design](../webgui.md) og [projektstruktur](../project-structure.md) for detaljer om implementeringen.

<!-- readme-unit:docs.extra -->
Funktionsvejledninger: [bærbar MCP](../portable-mcp.md), [indbyggede færdigheder](../builtin-skills.md) og [telemetri](../telemetry.md). Udviklingsplanlægning: [plan](../PLAN.md) og [køreplan](../ROADMAP.md).

<!-- readme-unit:reference -->
[Den tidligere fulde README](../readme-reference.md) bevares som historisk reference; ældre funktionsbeskrivelser kan være fra før version `1.0.0`.

<!-- readme-unit:h.build -->
## Byg og verificer

<!-- readme-unit:build -->
Brug Go-versionen angivet i `go.mod`. På Windows bygger `build.bat` TUI, `build_ui.bat` bygger GUI, og `run.bat` bygger efter behov og starter terminalen.

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
Kør Go-testene før en udgivelse. Dokumentationskontrollen verificerer alle 27 oversættelser, kortlagt indhold, overskrifter, links, tekniske litteraler og identiske kodeeksempler.

<!-- readme-unit:requirements -->
Normal brug kræver et konfigureret modelendpoint eller en konto. Git og ripgrep forbedrer arbejdsgange i repositories; eksterne værktøjer er valgfrie, medmindre en arbejdsgang kræver dem. GUI kræver desuden platformens understøttelse af webview/browser. Brug `--doctor` til at diagnosticere din installation.

<!-- readme-unit:h.license -->
## Licens

<!-- readme-unit:license -->
SuperCli bruger [MIT-licensen](../../LICENSE). Medfølgende afhængigheder og indhold har egne meddelelser; se [tredjepartsmeddelelser](../../THIRD_PARTY_NOTICES.md) og [færdighedsvejledningen](../builtin-skills.md).
