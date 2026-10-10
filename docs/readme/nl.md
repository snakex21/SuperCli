[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.5

<!-- readme-unit:intro -->
Een draagbare AI-programmeeragent geschreven in Go, met een terminalinterface (TUI), desktop-/webinterface (GUI) en batchmodus die één engine delen.

<!-- readme-unit:status -->
Deze README beschrijft versie `1.0.5`. Draagbare releasepakketten worden via [GitHub Releases](https://github.com/snakex21/SuperCli/releases) verspreid; een lokale build betekent niet dat de bijbehorende release al gepubliceerd is.

<!-- readme-unit:h.screenshots -->
## Schermafbeeldingen

<!-- readme-unit:screenshot.gui -->
![Webinterface van SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Actiecentrum in de SuperCli-terminal (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
De GUI-afbeelding is een schermafbeelding; de TUI-afbeelding is een weergave van de werkelijke terminalindeling van de toepassing. [Meer schermafbeeldingen](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Aan de slag

<!-- readme-unit:start -->
Pak het pakket voor uw besturingssysteem en architectuur uit in een schrijfbare map. Start op Windows `supercli.exe` voor de terminal of `supercli-web.exe` voor de GUI. Houd de meegeleverde map `supercli-data/` naast de uitvoerbare bestanden.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Gebruik op Linux of macOS het bijbehorende uitvoerbare bestand uit het pakket of bouw vanuit de broncode. Kies uw project met `--home`; gebruik `--batch` voor één verzoek zonder TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Draagbare gegevens

<!-- readme-unit:data -->
Instellingen, sessies, geheugen, inloggegevens, caches, logboeken en back-ups staan in `supercli-data/` naast de toepassing. Verplaats de hele toepassingsmap om ze mee te nemen. De toepassing gebruikt geen `%APPDATA%`, `%LOCALAPPDATA%` of Windows-register voor haar toestand.

<!-- readme-unit:workspace -->
`--home` en `SUPERCLI_HOME` kiezen de werkruimte zonder toepassingsgegevens te verplaatsen. Projectoverschrijvingen en werkbestanden gebruiken `<project>/.supercli/`.

<!-- readme-unit:override -->
Alleen expliciet ingestelde `--data-dir` of `SUPERCLI_DATA_DIR` wijzigen de gegevenslocatie. Als de toepassingsmap niet schrijfbaar is, meldt het opstarten een fout in plaats van ongemerkt een profielmap te gebruiken.

<!-- readme-unit:legacy -->
De terminal kan bij de eerste start oude `~/.supercli`-gegevens naar een lege draagbare map kopiëren en het origineel behouden. Zie [gegevensindeling](../data-layout.md).

<!-- readme-unit:secrets -->
Inloggegevens reizen mee met de draagbare map. Houd die privé, maak back-ups en leg nooit API-sleutels of authenticatiebestanden vast in een openbare repository.

<!-- readme-unit:h.config -->
## Modellen en configuratie

<!-- readme-unit:providers -->
Configureer providers via de GUI-instellingen of TUI `/providers` en `/models`. Ondersteunde verbindingen omvatten OpenAI-compatibele endpoints, native Anthropic, ChatGPT/Codex OAuth, opencode-gateways en een offline echo-provider.

<!-- readme-unit:config -->
Globale instellingen staan in `supercli-data/config.toml`; `<project>/.supercli/config.toml` kan ze overschrijven. Omgevingsvariabelen en CLI-opties hebben voorrang. Voorbeeld van een lokaal endpoint:

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
Vervang het voorbeeldendpoint en model door de waarden van uw server. Clouddiensten kunnen inloggegevens vereisen en gebruikskosten rekenen. Modelmogelijkheden bepalen beeldbegrip, tools, redeneren en contextlimieten. Zie [configuratie](../configuration.md).

<!-- readme-unit:h.features -->
## Functies

<!-- readme-unit:surface -->
GUI en TUI ondersteunen dezelfde 27 interfacetalen. Ze bieden gestreamde gesprekken, sessieherstel, projectkeuze, modelbeheer, bijlagen en gebruiksoverzichten. De TUI ondersteunt scrollen met de muis, zoeken in het gesprek en inklapbare denk-/tooluitvoer.

<!-- readme-unit:agent -->
De engine ondersteunt toolontdekking, gecontroleerde bestandswijzigingen, commando-uitvoering, sessiegeschiedenis, projectgeheugen, contextverdichting, doelen, delegatie aan werkagenten, consultatie en optionele conceptmodellen.

<!-- readme-unit:tools -->
Tools omvatten zoeken in code, gerichte bestandslezingen en patches, afbeeldingen, ZIP-archieven, DOCX/XLSX/PDF-documenten en begrensde contextuitvoering. Beschikbare tools hangen af van het gekozen profiel en model; gebruik toolontdekking voor de huidige catalogus.

<!-- readme-unit:extensions -->
Optionele MCP-pakketten staan in `supercli-data/mcp/` en starten bij gebruik. Het archief met ingebouwde vaardigheden staat in `supercli-data/skills/builtin-skills.zip`; ontbreken ervan verhindert normaal opstarten niet. Uitbreidingen kunnen eigen runtimes of geïnstalleerde hosttoepassingen nodig hebben.

<!-- readme-unit:optional -->
Darwin-kandidaatgeneratie, modelraden en delegatie aan werkagenten zijn optionele workflows. Parallelle modelaanroepen kunnen het resourcegebruik en de kosten verhogen. Het kernprogramma vereist geen Node, Python, Docker of CGO.

<!-- readme-unit:h.controls -->
## Commando's en bediening

<!-- readme-unit:controls -->
Typ `/` voor het commandopalet. Open het TUI-actiecentrum met `Tab` bij lege invoer of `Ctrl+K`; kies acties met de pijlen en keer terug met `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Commando | Actie |
| --- | --- |
| `/help`, `/doctor` | Help en diagnostiek tonen. |
| `/model`, `/models`, `/providers` | Modellen kiezen en providers beheren. |
| `/resume`, `/export` | Een gesprek hervatten of exporteren. |
| `/status`, `/cost`, `/compact` | Gebruik bekijken of context verdichten. |
| `/goal`, `/plan` | Doelen en alleen-lezen planmodus beheren. |
| `/diff`, `/undo`, `/redo` | Wijzigingen bekijken of een agentbeurt ongedaan maken/herhalen. |
| `/darwin`, `/council` | Optionele workflows met meerdere modellen gebruiken. |
| `/update check\|download\|install` | Een update controleren, downloaden of expliciet installeren. |
| `/quit`, `/exit` | De toepassing afsluiten. |

<!-- readme-unit:keys -->
`Enter` verstuurt een verzoek; `Ctrl+C` onderbreekt. `PgUp`/`PgDn` en het muiswiel scrollen; `Ctrl+F` zoekt; `Shift+T` wisselt de denkweergave; `Shift+E` breidt tooluitvoer uit. Verzoeken tijdens een uitvoering komen in de wachtrij voor een veilige volgende stap.

<!-- readme-unit:h.updates -->
## Updates

<!-- readme-unit:update.check -->
Controleer stabiele GitHub-releases op verzoek via het GUI-scherm About, TUI `/update check` of CLI `--check-update`. Controleren installeert niets.

<!-- readme-unit:update.download -->
Downloaden zet het bijpassende draagbare pakket klaar en controleert de SHA256 tegen het releasemanifest. GUI-download en `/update download` laten de actieve installatie staan.

<!-- readme-unit:update.install -->
Gebruik de GUI-installatieknop of `/update install` om de klaargezette update te installeren. CLI `--update` vraagt expliciet om download en installatie. Bestaande `supercli-data/` blijft behouden en vervangen uitvoerbare bestanden worden lokaal geback-upt.

<!-- readme-unit:update.restart -->
Herstart handmatig na installatie. Sluit andere kopieën vooraf; actieve uitvoerbare bestanden kunnen op Windows vergrendeld zijn. Gebruik een geverifieerde handmatige releasedownload als een passend pakket of verificatiemetadata ontbreekt. Bewaar uw eigen gegevensback-up.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Documentatie

<!-- readme-unit:docs.start -->
Begin met de [documentatie-index](../README.md), [snelstart](../quickstart.md), [gegevensindeling](../data-layout.md) en [configuratie](../configuration.md).

<!-- readme-unit:docs.engine -->
Lees [architectuur](../architecture.md), [delegatie](../delegation.md), [prestaties](../performance.md), [GUI-ontwerp](../webgui.md) en [projectstructuur](../project-structure.md) voor implementatiedetails.

<!-- readme-unit:docs.extra -->
Functiegidsen: [draagbare MCP](../portable-mcp.md), [ingebouwde vaardigheden](../builtin-skills.md) en [telemetrie](../telemetry.md). Ontwikkeling volgen: [plan](../PLAN.md) en [routekaart](../ROADMAP.md).

<!-- readme-unit:reference -->
De [vorige volledige README](../readme-reference.md) blijft als historische referentie behouden; oudere functiebeschrijvingen kunnen dateren van vóór versie `1.0.0`.

<!-- readme-unit:h.build -->
## Bouwen en controleren

<!-- readme-unit:build -->
Gebruik de Go-versie uit `go.mod`. Op Windows bouwt `build.bat` de TUI, `build_ui.bat` de GUI en bouwt `run.bat` indien nodig en start de terminal.

[docs/releasing.md](../releasing.md)

```bash
npm ci --ignore-scripts --no-audit --no-fund
go test ./...
go build -o supercli ./cmd/supercli
go build -o supercli-web ./cmd/supercli-web
node docs/readme/check.cjs
```

```powershell
npm ci --ignore-scripts --no-audit --no-fund
go test ./...
go build -o supercli.exe ./cmd/supercli
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
node docs/readme/check.cjs
```

<!-- readme-unit:tests -->
Voer de Go-tests uit vóór een release. De documentatiecontrole verifieert alle 27 vertalingen, gekoppelde inhoud, koppen, links, technische literals en identieke codevoorbeelden.

<!-- readme-unit:requirements -->
Normaal gebruik vereist een geconfigureerd modelendpoint of account. Git en ripgrep verbeteren repositoryworkflows; externe tools zijn optioneel tenzij een workflow ze nodig heeft. De GUI vereist bovendien webview-/browserondersteuning van het platform. Gebruik `--doctor` om uw installatie te onderzoeken.

<!-- readme-unit:h.license -->
## Licentie

<!-- readme-unit:license -->
SuperCli gebruikt de [MIT-licentie](../../LICENSE). Meegeleverde afhankelijkheden en inhoud hebben eigen vermeldingen; zie [vermeldingen van derden](../../THIRD_PARTY_NOTICES.md) en de [vaardighedengids](../builtin-skills.md).
