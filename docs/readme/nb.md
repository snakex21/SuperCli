[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
En portabel KI-kodingsagent skrevet i Go, med terminalgrensesnitt (TUI), skrivebords-/nettgrensesnitt (GUI) og satsvis modus som deler én motor.

<!-- readme-unit:status -->
Denne README beskriver versjon `1.0.0`. Portable utgivelsespakker distribueres gjennom [GitHub Releases](https://github.com/snakex21/SuperCli/releases); en lokal bygging betyr ikke at tilhørende utgivelse allerede er publisert.

<!-- readme-unit:h.screenshots -->
## Skjermbilder

<!-- readme-unit:screenshot.gui -->
![SuperClis nettgrensesnitt (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperClis handlingssenter i terminalen (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
GUI-bildet er et skjermbilde; TUI-bildet er en gjengivelse av programmets faktiske terminaloppsett. [Flere skjermbilder](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Kom i gang

<!-- readme-unit:start -->
Pakk ut pakken for operativsystemet og arkitekturen din i en skrivbar mappe. På Windows starter du `supercli.exe` for terminalen eller `supercli-web.exe` for GUI. Behold den medfølgende mappen `supercli-data/` ved siden av de kjørbare filene.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
På Linux eller macOS bruker du den tilsvarende kjørbare filen fra pakken eller bygger fra kildekoden. Velg prosjektet med `--home`; bruk `--batch` for én forespørsel uten TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Portable data

<!-- readme-unit:data -->
Innstillinger, økter, minne, påloggingsdata, hurtiglagre, logger og sikkerhetskopier ligger i `supercli-data/` ved siden av programmet. Flytt hele programmappen for å ta dem med. Programmet bruker ikke `%APPDATA%`, `%LOCALAPPDATA%` eller Windows-registeret til sin tilstand.

<!-- readme-unit:workspace -->
`--home` og `SUPERCLI_HOME` velger arbeidsområdet uten å flytte programdata. Prosjektoverstyringer og arbeidsfiler bruker `<project>/.supercli/`.

<!-- readme-unit:override -->
Bare uttrykkelig angitt `--data-dir` eller `SUPERCLI_DATA_DIR` endrer dataplasseringen. Hvis programmappen ikke er skrivbar, melder oppstarten en feil i stedet for å bruke en profilmappe uten varsel.

<!-- readme-unit:legacy -->
Terminalen kan ved første start kopiere eldre `~/.supercli`-data til en tom portabel mappe og beholde originalen. Se [dataoppsett](../data-layout.md).

<!-- readme-unit:secrets -->
Påloggingsdata følger den portable mappen. Hold den privat, ta sikkerhetskopier, og legg aldri API-nøkler eller autentiseringsfiler i et offentlig kodelager.

<!-- readme-unit:h.config -->
## Modeller og konfigurasjon

<!-- readme-unit:providers -->
Konfigurer leverandører via GUI-innstillingene eller TUI `/providers` og `/models`. Støttede forbindelser omfatter OpenAI-kompatible endepunkter, innebygd Anthropic, ChatGPT/Codex OAuth, opencode-portaler og en frakoblet echo-leverandør.

<!-- readme-unit:config -->
Globale innstillinger ligger i `supercli-data/config.toml`; `<project>/.supercli/config.toml` kan overstyre dem. Miljøvariabler og CLI-flagg har forrang. Eksempel på et lokalt endepunkt:

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
Bytt ut eksempelets endepunkt og modell med serverens verdier. Skytjenester kan kreve påloggingsdata og ta betalt for bruk. Modellens egenskaper bestemmer bildeanalyse, verktøy, resonnering og kontekstgrenser. Se [konfigurasjon](../configuration.md).

<!-- readme-unit:h.features -->
## Funksjoner

<!-- readme-unit:surface -->
GUI og TUI støtter de samme 27 grensesnittspråkene. De tilbyr strømmede samtaler, øktgjenoppretting, prosjektvalg, modelladministrasjon, vedlegg og bruksoversikter. TUI støtter muserulling, søk i samtalen og sammenleggbar tenke-/verktøyutdata.

<!-- readme-unit:agent -->
Motoren støtter verktøyoppdagelse, verifiserte filendringer, kommandokjøring, økthistorikk, prosjektminne, kontekstkomprimering, mål, delegering til arbeidsagenter, konsultasjon og valgfrie utkastmodeller.

<!-- readme-unit:tools -->
Verktøyene dekker kodesøk, målrettet fillesing og retting, bilder, ZIP-arkiver, DOCX/XLSX/PDF-dokumenter og avgrenset kontekstkjøring. Tilgjengelige verktøy avhenger av valgt profil og modell; bruk verktøyoppdagelse for gjeldende katalog.

<!-- readme-unit:extensions -->
Valgfrie MCP-pakker ligger i `supercli-data/mcp/` og starter når de brukes. Arkivet med innebygde ferdigheter ligger i `supercli-data/skills/builtin-skills.zip`; mangler det, hindrer det ikke vanlig oppstart. Utvidelser kan kreve egne kjøremiljøer eller installerte vertsprogrammer.

<!-- readme-unit:optional -->
Darwin-kandidatgenerering, modellråd og delegering til arbeidsagenter er valgfrie arbeidsflyter. Parallelle modellkall kan øke ressursbruk og kostnader. Den sentrale kjørbare filen krever ikke Node, Python, Docker eller CGO.

<!-- readme-unit:h.controls -->
## Kommandoer og betjening

<!-- readme-unit:controls -->
Skriv `/` for kommandopaletten. Åpne TUI-handlingssenteret med `Tab` ved tom inndata eller `Ctrl+K`; velg handlinger med piltastene og gå tilbake med `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Kommando | Handling |
| --- | --- |
| `/help`, `/doctor` | Vis hjelp og diagnostikk. |
| `/model`, `/models`, `/providers` | Velg modeller og administrer leverandører. |
| `/resume`, `/export` | Fortsett eller eksporter en samtale. |
| `/status`, `/cost`, `/compact` | Undersøk bruk eller komprimer kontekst. |
| `/goal`, `/plan` | Administrer mål og skrivebeskyttet planmodus. |
| `/diff`, `/undo`, `/redo` | Undersøk endringer eller angre/gjenta en agenttur. |
| `/darwin`, `/council` | Bruk valgfrie arbeidsflyter med flere modeller. |
| `/update check\|download\|install` | Se etter, last ned eller installer en oppdatering uttrykkelig. |
| `/quit`, `/exit` | Avslutt programmet. |

<!-- readme-unit:keys -->
`Enter` sender en forespørsel; `Ctrl+C` avbryter. `PgUp`/`PgDn` og musehjulet ruller; `Ctrl+F` søker; `Shift+T` veksler tenkevisning; `Shift+E` utvider verktøyutdata. Forespørsler skrevet under en kjøring settes i kø til neste trygge trinn.

<!-- readme-unit:h.updates -->
## Oppdateringer

<!-- readme-unit:update.check -->
Se etter stabile GitHub-utgivelser ved behov fra GUI-skjermen About, TUI `/update check` eller CLI `--check-update`. Kontrollen installerer ingenting.

<!-- readme-unit:update.download -->
Nedlastingen klargjør den passende portable pakken og verifiserer dens SHA256 mot utgivelsesmanifestet. GUI-nedlasting og `/update download` lar den kjørende installasjonen bli stående.

<!-- readme-unit:update.install -->
Bruk GUI-installasjonsknappen eller `/update install` for å installere den klargjorte oppdateringen. CLI `--update` ber uttrykkelig om nedlasting og installasjon. Eksisterende `supercli-data/` beholdes, og erstattede kjørbare filer sikkerhetskopieres lokalt.

<!-- readme-unit:update.restart -->
Start på nytt manuelt etter installasjon. Lukk andre kopier før installasjon; kjørende filer kan være låst på Windows. Hvis passende pakke eller verifikasjonsmetadata mangler, bruk en verifisert manuell utgivelsesnedlasting. Behold din egen sikkerhetskopi av dataene.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentasjon

<!-- readme-unit:docs.start -->
Begynn med [dokumentasjonsindeksen](../README.md), [hurtigstart](../quickstart.md), [dataoppsett](../data-layout.md) og [konfigurasjon](../configuration.md).

<!-- readme-unit:docs.engine -->
Les [arkitektur](../architecture.md), [delegering](../delegation.md), [ytelse](../performance.md), [GUI-design](../webgui.md) og [prosjektstruktur](../project-structure.md) for implementasjonsdetaljer.

<!-- readme-unit:docs.extra -->
Funksjonsveiledninger: [portabel MCP](../portable-mcp.md), [innebygde ferdigheter](../builtin-skills.md) og [telemetri](../telemetry.md). Utviklingssporing: [plan](../PLAN.md) og [veikart](../ROADMAP.md).

<!-- readme-unit:reference -->
[Den tidligere komplette README](../readme-reference.md) beholdes som historisk referanse; eldre funksjonsbeskrivelser kan være fra før versjon `1.0.0`.

<!-- readme-unit:h.build -->
## Bygg og verifiser

<!-- readme-unit:build -->
Bruk Go-versjonen angitt i `go.mod`. På Windows bygger `build.bat` TUI, `build_ui.bat` GUI, og `run.bat` bygger ved behov og starter terminalen.

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
Kjør Go-testene før en utgivelse. Dokumentasjonskontrollen verifiserer alle 27 oversettelser, tilordnet innhold, overskrifter, lenker, tekniske literaler og identiske kodeeksempler.

<!-- readme-unit:requirements -->
Vanlig bruk krever et konfigurert modellendepunkt eller en konto. Git og ripgrep forbedrer arbeidsflyter i kodelagre; eksterne verktøy er valgfrie med mindre en arbeidsflyt trenger dem. GUI krever dessuten plattformens støtte for webview/nettleser. Bruk `--doctor` for å diagnostisere installasjonen.

<!-- readme-unit:h.license -->
## Lisens

<!-- readme-unit:license -->
SuperCli bruker [MIT-lisensen](../../LICENSE). Medfølgende avhengigheter og innhold har egne merknader; se [tredjepartsmerknader](../../THIRD_PARTY_NOTICES.md) og [ferdighetsveiledningen](../builtin-skills.md).
