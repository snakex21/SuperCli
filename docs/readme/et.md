[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.3

<!-- readme-unit:intro -->
Go keeles kirjutatud kaasaskantav AI programmeerimisagent, mille terminaliliides (TUI), töölaua-/veebiliides (GUI) ja pakktöötlusrežiim kasutavad ühist mootorit.

<!-- readme-unit:status -->
See README kirjeldab versiooni `1.0.3`. Kaasaskantavaid väljalaskepakette levitatakse [GitHub Releases](https://github.com/snakex21/SuperCli/releases) kaudu; kohalik ehitus ei tähenda, et vastav väljalase oleks juba avaldatud.

<!-- readme-unit:h.screenshots -->
## Kuvatõmmised

<!-- readme-unit:screenshot.gui -->
![SuperCli veebiliides (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperCli terminali tegevuskeskus (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
GUI pilt on kuvatõmmis; TUI pilt on rakenduse tegeliku terminalipaigutuse renderdus. [Rohkem kuvatõmmiseid](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Alustamine

<!-- readme-unit:start -->
Paki oma operatsioonisüsteemi ja arhitektuuri pakett lahti kirjutatavasse kausta. Windowsis käivita terminali jaoks `supercli.exe` või GUI jaoks `supercli-web.exe`. Hoia kaasas olevat `supercli-data/` kausta käivitatavate failide kõrval.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Linuxis või macOS-is kasuta paketi vastavat käivitatavat faili või ehita lähtekoodist. Vali projekt valikuga `--home`; ühe päringu jaoks ilma TUI-ta kasuta `--batch`.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Kaasaskantavad andmed

<!-- readme-unit:data -->
Seaded, seansid, mälu, autentimisandmed, vahemälud, logid ja varukoopiad asuvad rakenduse kõrval kaustas `supercli-data/`. Nende kaasavõtmiseks liiguta kogu rakenduse kaust. Rakendus ei kasuta oma oleku jaoks `%APPDATA%`, `%LOCALAPPDATA%` ega Windowsi registrit.

<!-- readme-unit:workspace -->
`--home` ja `SUPERCLI_HOME` valivad tööruumi rakenduse andmeid liigutamata. Projekti seadistuse ülekirjutused ja tööruumi failid kasutavad `<project>/.supercli/`.

<!-- readme-unit:override -->
Andmete asukohta muudab ainult selgesõnaline `--data-dir` või `SUPERCLI_DATA_DIR`. Kui rakenduse kaust pole kirjutatav, teatab käivitus veast ega kasuta vaikselt profiilikausta.

<!-- readme-unit:legacy -->
Terminal võib esimesel käivitusel kopeerida varasemad `~/.supercli` andmed tühja kaasaskantavasse kausta, säilitades originaali. Vaata [andmete paigutust](../data-layout.md).

<!-- readme-unit:secrets -->
Autentimisandmed liiguvad kaasaskantava kaustaga. Hoia kaust privaatsena, varunda seda ning ära lisa API võtmeid ega autentimisfaile avalikku repositooriumisse.

<!-- readme-unit:h.config -->
## Mudelid ja seadistamine

<!-- readme-unit:providers -->
Seadista teenusepakkujad GUI seadetes või TUI `/providers` ja `/models` kaudu. Toetatud ühendused hõlmavad OpenAI-ühilduvaid otspunkte, natiivset Anthropicut, ChatGPT/Codex OAuthi, opencode'i lüüse ja võrguühenduseta echo-pakkujat.

<!-- readme-unit:config -->
Üldseaded asuvad failis `supercli-data/config.toml`; `<project>/.supercli/config.toml` saab neid üle kirjutada. Keskkonnamuutujatel ja CLI valikutel on eelis. Kohaliku otspunkti näide:

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
Asenda näite otspunkt ja mudel oma serveri väärtustega. Pilveteenused võivad nõuda autentimisandmeid ja kasutamise eest tasu võtta. Mudeli võimalused määravad pildimõistmise, tööriistad, arutlemise ja kontekstipiirid. Vaata [seadistamist](../configuration.md).

<!-- readme-unit:h.features -->
## Funktsioonid

<!-- readme-unit:surface -->
GUI ja TUI toetavad samu 27 liidesekeelt. Need pakuvad voogedastatavaid vestlusi, seansitaastet, projektivalikut, mudelihaldust, manuseid ja kasutusvaateid. TUI toetab hiirega kerimist, vestlusotsingut ning kokkupandavat mõtlemise/tööriistade väljundit.

<!-- readme-unit:agent -->
Mootor toetab tööriistade leidmist, kontrollitud failimuudatusi, käskude täitmist, seansiajalugu, projektimälu, konteksti tihendamist, eesmärke, tööagentidele delegeerimist, konsulteerimist ja valikulisi mustandimudeleid.

<!-- readme-unit:tools -->
Tööriistad hõlmavad koodiotsingut, sihitud faililugemist ja paikamist, pilte, ZIP-arhiive, DOCX/XLSX/PDF-dokumente ja piiratud kontekstikäivitust. Saadaolevad tööriistad sõltuvad valitud profiilist ja mudelist; kasuta tööriistaotsingut kehtiva kataloogi leidmiseks.

<!-- readme-unit:extensions -->
Valikulised MCP-paketid asuvad `supercli-data/mcp/` all ja käivituvad kasutamisel. Sisseehitatud oskuste arhiiv asub `supercli-data/skills/builtin-skills.zip` all; selle puudumine ei takista tavakäivitust. Laiendused võivad vajada oma käituskeskkondi või paigaldatud hostirakendusi.

<!-- readme-unit:optional -->
Darwini kandidaatide loomine, mudelinõukogud ja tööagentidele delegeerimine on valikulised töövood. Paralleelsed mudelikutsed võivad suurendada ressursikulu ja hinda. Põhikäivitatav fail ei vaja Node'i, Pythonit, Dockerit ega CGO-d.

<!-- readme-unit:h.controls -->
## Käsud ja juhtimine

<!-- readme-unit:controls -->
Käsupaleti avamiseks sisesta `/`. Ava TUI tegevuskeskus tühja sisendi korral klahviga `Tab` või `Ctrl+K`; vali tegevusi nooltega ja naase klahviga `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Käsk | Tegevus |
| --- | --- |
| `/help`, `/doctor` | Kuva abi ja diagnostika. |
| `/model`, `/models`, `/providers` | Vali mudeleid ja halda teenusepakkujaid. |
| `/resume`, `/export` | Jätka või ekspordi vestlus. |
| `/status`, `/cost`, `/compact` | Vaata kasutust või tihenda konteksti. |
| `/goal`, `/plan` | Halda eesmärke ja kirjutuskaitstud plaanirežiimi. |
| `/diff`, `/undo`, `/redo` | Vaata muudatusi või võta agendisamm tagasi/tee uuesti. |
| `/darwin`, `/council` | Kasuta valikulisi mitme mudeli töövooge. |
| `/update check\|download\|install` | Kontrolli, laadi alla või paigalda uuendus selgesõnaliselt. |
| `/quit`, `/exit` | Välju rakendusest. |

<!-- readme-unit:keys -->
`Enter` saadab päringu; `Ctrl+C` katkestab. `PgUp`/`PgDn` ja hiireratas kerivad; `Ctrl+F` otsib; `Shift+T` lülitab mõtlemisvaadet; `Shift+E` laiendab tööriistaväljundit. Käivituse ajal sisestatud päringud lähevad järjekorda järgmise turvalise sammu jaoks.

<!-- readme-unit:h.updates -->
## Uuendused

<!-- readme-unit:update.check -->
Kontrolli stabiilseid GitHubi väljalaskeid vajaduse korral GUI About-kuvalt, TUI `/update check` või CLI `--check-update` abil. Kontroll ei paigalda midagi.

<!-- readme-unit:update.download -->
Allalaadimine valmistab sobiva kaasaskantava paketi ette ja kontrollib selle SHA256 väärtust väljalaskemanifesti vastu. GUI allalaadimine ja `/update download` jätavad töötava paigalduse alles.

<!-- readme-unit:update.install -->
Ettevalmistatud uuenduse paigaldamiseks kasuta GUI paigaldusnuppu või `/update install`. CLI `--update` nõuab selgesõnaliselt allalaadimist ja paigaldamist. Olemasolev `supercli-data/` säilib ning asendatud käivitatavatest failidest tehakse kohalikud varukoopiad.

<!-- readme-unit:update.restart -->
Pärast paigaldamist taaskäivita käsitsi. Sulge enne teised koopiad; töötavad käivitatavad failid võivad Windowsis lukus olla. Kui sobiv pakett või kontrolli metaandmed puuduvad, kasuta kontrollitud käsitsi allalaaditud väljalaset. Hoia oma andmetest eraldi varukoopiat.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentatsioon

<!-- readme-unit:docs.start -->
Alusta [dokumentatsiooni sisukorrast](../README.md), [kiirjuhendist](../quickstart.md), [andmete paigutusest](../data-layout.md) ja [seadistamisest](../configuration.md).

<!-- readme-unit:docs.engine -->
Teostuse üksikasju kirjeldavad [arhitektuur](../architecture.md), [delegeerimine](../delegation.md), [jõudlus](../performance.md), [GUI kujundus](../webgui.md) ja [projekti struktuur](../project-structure.md).

<!-- readme-unit:docs.extra -->
Funktsioonijuhendid: [kaasaskantav MCP](../portable-mcp.md), [sisseehitatud oskused](../builtin-skills.md) ja [telemeetria](../telemetry.md). Arenduse jälgimine: [plaan](../PLAN.md) ja [teekaart](../ROADMAP.md).

<!-- readme-unit:reference -->
[Eelmine täielik README](../readme-reference.md) säilib ajaloolise viitena; vanemad funktsioonikirjeldused võivad pärineda versioonist `1.0.0` varasemast ajast.

<!-- readme-unit:h.build -->
## Ehitamine ja kontrollimine

<!-- readme-unit:build -->
Kasuta failis `go.mod` määratud Go versiooni. Windowsis ehitab `build.bat` TUI, `build_ui.bat` GUI ning `run.bat` ehitab vajadusel ja käivitab terminali.

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
Enne väljalaset käivita Go testid. Dokumentatsioonikontroll kontrollib kõiki 27 tõlget, seotud sisu, pealkirju, linke, tehnilisi literaale ja identseid koodinäiteid.

<!-- readme-unit:requirements -->
Tavakasutus vajab seadistatud mudeli otspunkti või kontot. Git ja ripgrep parandavad repositooriumitöövooge; välised tööriistad on valikulised, kui töövoog neid ei vaja. GUI vajab lisaks platvormi webview/brauserituge. Paigalduse diagnoosimiseks kasuta `--doctor`.

<!-- readme-unit:h.license -->
## Litsents

<!-- readme-unit:license -->
SuperCli kasutab [MIT-litsentsi](../../LICENSE). Kaasasolevatel sõltuvustel ja sisul on oma teatised; vaata [kolmandate osapoolte teatisi](../../THIRD_PARTY_NOTICES.md) ja [oskuste juhendit](../builtin-skills.md).
