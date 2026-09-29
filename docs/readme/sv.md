[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
En portabel AI-kodningsagent skriven i Go, med terminalgränssnitt (TUI), skrivbords-/webbgränssnitt (GUI) och batchläge som delar en motor.

<!-- readme-unit:status -->
Denna README beskriver version `1.0.0`. Portabla utgivningspaket distribueras via [GitHub Releases](https://github.com/snakex21/SuperCli/releases); en lokal kompilering innebär inte att motsvarande version redan har publicerats.

<!-- readme-unit:h.screenshots -->
## Skärmbilder

<!-- readme-unit:screenshot.gui -->
![SuperClis webbgränssnitt (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperClis åtgärdscenter i terminalen (TUI)](../screenshots/1.0.0/tui-actions-pl.jpg)

<!-- readme-unit:screenshots.more -->
GUI-bilden är en skärmbild; TUI-bilden är en rendering av programmets faktiska terminallayout. [Fler skärmbilder](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Kom igång

<!-- readme-unit:start -->
Packa upp paketet för ditt operativsystem och din arkitektur i en skrivbar mapp. På Windows startar du `supercli.exe` för terminalen eller `supercli-web.exe` för GUI. Behåll den medföljande mappen `supercli-data/` bredvid de körbara filerna.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
På Linux eller macOS använder du motsvarande körbara fil från paketet eller bygger från källkoden. Välj projekt med `--home`; använd `--batch` för en begäran utan TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Portabla data

<!-- readme-unit:data -->
Inställningar, sessioner, minne, inloggningsuppgifter, cache, loggar och säkerhetskopior finns i `supercli-data/` bredvid programmet. Flytta hela programmappen för att ta dem med dig. Programmet använder inte `%APPDATA%`, `%LOCALAPPDATA%` eller Windows-registret för sitt tillstånd.

<!-- readme-unit:workspace -->
`--home` och `SUPERCLI_HOME` väljer arbetsområdet utan att flytta programdata. Projektåsidosättningar och arbetsfiler använder `<project>/.supercli/`.

<!-- readme-unit:override -->
Endast uttryckligt angivna `--data-dir` eller `SUPERCLI_DATA_DIR` ändrar dataplatsen. Om programmappen inte är skrivbar rapporterar starten ett fel i stället för att tyst använda en profilmapp.

<!-- readme-unit:legacy -->
Terminalen kan vid första starten kopiera äldre `~/.supercli`-data till en tom portabel katalog och behålla originalet. Se [datalayout](../data-layout.md).

<!-- readme-unit:secrets -->
Inloggningsuppgifter följer den portabla mappen. Håll den privat, säkerhetskopiera den och lägg aldrig API-nycklar eller autentiseringsfiler i ett offentligt kodarkiv.

<!-- readme-unit:h.config -->
## Modeller och konfiguration

<!-- readme-unit:providers -->
Konfigurera leverantörer via GUI-inställningarna eller TUI `/providers` och `/models`. Stödda anslutningar omfattar OpenAI-kompatibla ändpunkter, inbyggd Anthropic, ChatGPT/Codex OAuth, opencode-gatewayer och en offline echo-leverantör.

<!-- readme-unit:config -->
Globala inställningar finns i `supercli-data/config.toml`; `<project>/.supercli/config.toml` kan åsidosätta dem. Miljövariabler och CLI-flaggor har företräde. Exempel på en lokal ändpunkt:

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
Ersätt exemplets ändpunkt och modell med serverns värden. Molntjänster kan kräva inloggningsuppgifter och ta betalt för användning. Modellens förmågor bestämmer bildförståelse, verktyg, resonemang och kontextgränser. Se [konfiguration](../configuration.md).

<!-- readme-unit:h.features -->
## Funktioner

<!-- readme-unit:surface -->
GUI och TUI stöder samma 27 gränssnittsspråk. De erbjuder strömmade samtal, sessionsåterställning, projektval, modellhantering, bilagor och användningsvyer. TUI stöder musrullning, sökning i samtalet och hopfällbara tanke-/verktygsutdata.

<!-- readme-unit:agent -->
Motorn stöder verktygsupptäckt, verifierade filändringar, kommandokörning, sessionshistorik, projektminne, kontextkomprimering, mål, delegering till arbetsagenter, konsultation och valfria utkastmodeller.

<!-- readme-unit:tools -->
Verktygen omfattar kodsökning, riktad filläsning och rättning, bilder, ZIP-arkiv, DOCX/XLSX/PDF-dokument och avgränsad kontextkörning. Tillgängliga verktyg beror på vald profil och modell; använd verktygsupptäckt för den aktuella katalogen.

<!-- readme-unit:extensions -->
Valfria MCP-paket finns i `supercli-data/mcp/` och startar när de används. Arkivet med inbyggda färdigheter finns i `supercli-data/skills/builtin-skills.zip`; frånvaro hindrar inte normal start. Tillägg kan behöva egna körmiljöer eller installerade värdprogram.

<!-- readme-unit:optional -->
Darwin-kandidatgenerering, modellråd och delegering till arbetsagenter är valfria arbetsflöden. Parallella modellanrop kan öka resursanvändning och kostnad. Kärnprogrammet kräver inte Node, Python, Docker eller CGO.

<!-- readme-unit:h.controls -->
## Kommandon och kontroller

<!-- readme-unit:controls -->
Skriv `/` för kommandopaletten. Öppna TUI-åtgärdscentret med `Tab` vid tom inmatning eller `Ctrl+K`; välj åtgärder med pilarna och återgå med `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Kommando | Åtgärd |
| --- | --- |
| `/help`, `/doctor` | Visa hjälp och diagnostik. |
| `/model`, `/models`, `/providers` | Välj modeller och hantera leverantörer. |
| `/resume`, `/export` | Återuppta eller exportera ett samtal. |
| `/status`, `/cost`, `/compact` | Granska användning eller komprimera kontext. |
| `/goal`, `/plan` | Hantera mål och skrivskyddat planläge. |
| `/diff`, `/undo`, `/redo` | Granska ändringar eller ångra/gör om en agenttur. |
| `/darwin`, `/council` | Använd valfria arbetsflöden med flera modeller. |
| `/update check\|download\|install` | Kontrollera, hämta eller installera uttryckligen en uppdatering. |
| `/quit`, `/exit` | Avsluta programmet. |

<!-- readme-unit:keys -->
`Enter` skickar en begäran; `Ctrl+C` avbryter. `PgUp`/`PgDn` och mushjulet rullar; `Ctrl+F` söker; `Shift+T` växlar tankevisning; `Shift+E` utökar verktygsutdata. Begäranden som skrivs under en körning köas till nästa säkra steg.

<!-- readme-unit:h.updates -->
## Uppdateringar

<!-- readme-unit:update.check -->
Kontrollera stabila GitHub-utgåvor på begäran från GUI-skärmen About, TUI `/update check` eller CLI `--check-update`. Kontroll installerar ingenting.

<!-- readme-unit:update.download -->
Hämtningen förbereder motsvarande portabla paket och verifierar dess SHA256 mot utgivningsmanifestet. GUI-hämtning och `/update download` låter den körande installationen ligga kvar.

<!-- readme-unit:update.install -->
Använd GUI-installationsknappen eller `/update install` för den förberedda uppdateringen. CLI `--update` begär uttryckligen hämtning och installation. Befintlig `supercli-data/` bevaras och ersatta körbara filer säkerhetskopieras lokalt.

<!-- readme-unit:update.restart -->
Starta om manuellt efter installationen. Stäng andra kopior före installation; körande filer kan vara låsta på Windows. Om motsvarande paket eller verifieringsmetadata saknas, använd en verifierad manuell utgivningshämtning. Behåll en egen säkerhetskopia av data.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentation

<!-- readme-unit:docs.start -->
Börja med [dokumentationsindex](../README.md), [snabbstart](../quickstart.md), [datalayout](../data-layout.md) och [konfiguration](../configuration.md).

<!-- readme-unit:docs.engine -->
Läs [arkitektur](../architecture.md), [delegering](../delegation.md), [prestanda](../performance.md), [GUI-design](../webgui.md) och [projektstruktur](../project-structure.md) för implementationsdetaljer.

<!-- readme-unit:docs.extra -->
Funktionsguider: [portabel MCP](../portable-mcp.md), [inbyggda färdigheter](../builtin-skills.md) och [telemetri](../telemetry.md). Utvecklingsuppföljning: [plan](../PLAN.md) och [färdplan](../ROADMAP.md).

<!-- readme-unit:reference -->
[Den tidigare fullständiga README](../readme-reference.md) bevaras som historisk referens; äldre funktionsbeskrivningar kan vara från före version `1.0.0`.

<!-- readme-unit:h.build -->
## Bygg och verifiera

<!-- readme-unit:build -->
Använd Go-versionen i `go.mod`. På Windows bygger `build.bat` TUI, `build_ui.bat` GUI, och `run.bat` bygger vid behov och startar terminalen.

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
Kör Go-testerna före en utgåva. Dokumentationskontrollen verifierar alla 27 översättningar, mappat innehåll, rubriker, länkar, tekniska literaler och identiska kodexempel.

<!-- readme-unit:requirements -->
Normal användning kräver en konfigurerad modelländpunkt eller ett konto. Git och ripgrep förbättrar arbetsflöden i kodarkiv; externa verktyg är valfria om inte arbetsflödet behöver dem. GUI kräver dessutom plattformens stöd för webview/webbläsare. Använd `--doctor` för att diagnostisera installationen.

<!-- readme-unit:h.license -->
## Licens

<!-- readme-unit:license -->
SuperCli använder [MIT-licensen](../../LICENSE). Medföljande beroenden och innehåll har egna meddelanden; se [tredjepartsmeddelanden](../../THIRD_PARTY_NOTICES.md) och [färdighetsguiden](../builtin-skills.md).
