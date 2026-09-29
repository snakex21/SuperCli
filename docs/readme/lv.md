[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Go valodā rakstīts pārnēsājams AI programmēšanas aģents ar termināļa saskarni (TUI), darbvirsmas/tīmekļa saskarni (GUI) un pakešu režīmu, kas izmanto vienu dzinēju.

<!-- readme-unit:status -->
Šis README apraksta versiju `1.0.0`. Pārnēsājamos laidienu komplektus izplata caur [GitHub Releases](https://github.com/snakex21/SuperCli/releases); lokāla būvēšana nenozīmē, ka attiecīgais laidiens jau ir publicēts.

<!-- readme-unit:h.start -->
## Darba sākšana

<!-- readme-unit:start -->
Izpakojiet savai operētājsistēmai un arhitektūrai atbilstošo komplektu rakstāmā mapē. Windows sistēmā palaidiet `supercli.exe` terminālim vai `supercli-web.exe` GUI. Iekļauto `supercli-data/` mapi turiet līdzās izpildāmajiem failiem.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Linux vai macOS sistēmā izmantojiet atbilstošo izpildāmo failu no komplekta vai būvējiet no pirmkoda. Izvēlieties projektu ar `--home`; vienam pieprasījumam bez TUI izmantojiet `--batch`.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Pārnēsājami dati

<!-- readme-unit:data -->
Iestatījumi, sesijas, atmiņa, akreditācijas dati, kešatmiņas, žurnāli un rezerves kopijas atrodas `supercli-data/` līdzās lietotnei. Pārvietojiet visu lietotnes mapi, lai paņemtu tos līdzi. Lietotne savam stāvoklim neizmanto `%APPDATA%`, `%LOCALAPPDATA%` vai Windows reģistru.

<!-- readme-unit:workspace -->
`--home` un `SUPERCLI_HOME` izvēlas darbvietu, nepārvietojot lietotnes datus. Projekta pārrakstošie iestatījumi un darba faili izmanto `<project>/.supercli/`.

<!-- readme-unit:override -->
Datu atrašanās vietu maina tikai skaidri norādīts `--data-dir` vai `SUPERCLI_DATA_DIR`. Ja lietotnes mapē nevar rakstīt, palaišana ziņo par kļūdu, nevis nemanāmi izmanto profila mapi.

<!-- readme-unit:legacy -->
Pirmajā palaišanā terminālis var kopēt vecos `~/.supercli` datus uz tukšu pārnēsājamu direktoriju, saglabājot oriģinālu. Skatiet [datu izkārtojumu](../data-layout.md).

<!-- readme-unit:secrets -->
Akreditācijas dati ceļo kopā ar pārnēsājamo mapi. Turiet to privātu, veidojiet rezerves kopijas un nekad neiekļaujiet API atslēgas vai autentifikācijas failus publiskā repozitorijā.

<!-- readme-unit:h.config -->
## Modeļi un konfigurācija

<!-- readme-unit:providers -->
Konfigurējiet pakalpojumu sniedzējus GUI iestatījumos vai TUI `/providers` un `/models`. Tiek atbalstīti ar OpenAI saderīgi galapunkti, vietējais Anthropic savienojums, ChatGPT/Codex OAuth, opencode vārtejas un bezsaistes echo sniedzējs.

<!-- readme-unit:config -->
Globālie iestatījumi ir `supercli-data/config.toml`; `<project>/.supercli/config.toml` var tos pārrakstīt. Vides mainīgajiem un CLI opcijām ir prioritāte. Vietēja galapunkta piemērs:

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
Aizstājiet piemēra galapunktu un modeli ar sava servera vērtībām. Mākoņpakalpojumi var pieprasīt akreditācijas datus un iekasēt maksu par lietošanu. Modeļa spējas nosaka attēlu izpratni, rīkus, spriešanu un konteksta ierobežojumus. Skatiet [konfigurāciju](../configuration.md).

<!-- readme-unit:h.features -->
## Iespējas

<!-- readme-unit:surface -->
GUI un TUI atbalsta tās pašas 27 saskarnes valodas. Tās nodrošina straumētas sarunas, sesiju atjaunošanu, projektu izvēli, modeļu pārvaldību, pielikumus un lietojuma skatus. TUI atbalsta ritināšanu ar peli, meklēšanu sarunā un sakļaujamu domāšanas/rīku izvadi.

<!-- readme-unit:agent -->
Dzinējs atbalsta rīku atklāšanu, pārbaudītus failu labojumus, komandu izpildi, sesiju vēsturi, projekta atmiņu, konteksta sablīvēšanu, mērķus, deleģēšanu darba aģentiem, konsultācijas un izvēles uzmetumu modeļus.

<!-- readme-unit:tools -->
Rīki aptver koda meklēšanu, mērķētus failu lasījumus un ielāpus, attēlus, ZIP arhīvus, DOCX/XLSX/PDF dokumentus un ierobežotu konteksta izpildi. Pieejamie rīki atkarīgi no izvēlētā profila un modeļa; pašreizējo katalogu iegūstiet ar rīku atklāšanu.

<!-- readme-unit:extensions -->
Izvēles MCP pakotnes atrodas `supercli-data/mcp/` un sāk darboties lietošanas brīdī. Iebūvēto prasmju arhīvs ir `supercli-data/skills/builtin-skills.zip`; tā trūkums netraucē parastai palaišanai. Paplašinājumiem var vajadzēt savas izpildvides vai instalētas saimnieklietotnes.

<!-- readme-unit:optional -->
Darwin kandidātu ģenerēšana, modeļu padomes un deleģēšana darba aģentiem ir izvēles darbplūsmas. Paralēli modeļu izsaukumi var palielināt resursu patēriņu un izmaksas. Pamata izpildāmais fails neprasa Node, Python, Docker vai CGO.

<!-- readme-unit:h.controls -->
## Komandas un vadība

<!-- readme-unit:controls -->
Komandu paletei ievadiet `/`. TUI darbību centru atveriet ar `Tab` tukšā ievadē vai `Ctrl+K`; izvēlieties darbības ar bultiņām un atgriezieties ar `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Komanda | Darbība |
| --- | --- |
| `/help`, `/doctor` | Rādīt palīdzību un diagnostiku. |
| `/model`, `/models`, `/providers` | Izvēlēties modeļus un pārvaldīt sniedzējus. |
| `/resume`, `/export` | Turpināt vai eksportēt sarunu. |
| `/status`, `/cost`, `/compact` | Pārskatīt lietojumu vai sablīvēt kontekstu. |
| `/goal`, `/plan` | Pārvaldīt mērķus un tikai lasāmu plānošanas režīmu. |
| `/diff`, `/undo`, `/redo` | Pārskatīt izmaiņas vai atsaukt/atkārtot aģenta gājienu. |
| `/darwin`, `/council` | Izmantot izvēles vairāku modeļu darbplūsmas. |
| `/update check\|download\|install` | Pārbaudīt, lejupielādēt vai skaidri instalēt atjauninājumu. |
| `/quit`, `/exit` | Iziet no lietotnes. |

<!-- readme-unit:keys -->
`Enter` nosūta pieprasījumu; `Ctrl+C` pārtrauc. `PgUp`/`PgDn` un peles ritenītis ritina; `Ctrl+F` meklē; `Shift+T` pārslēdz domāšanu; `Shift+E` izvērš rīku izvadi. Darbības laikā ievadītie pieprasījumi tiek ievietoti rindā nākamajam drošajam solim.

<!-- readme-unit:h.updates -->
## Atjauninājumi

<!-- readme-unit:update.check -->
Stabilos GitHub laidienus pārbaudiet pēc pieprasījuma GUI About ekrānā, TUI `/update check` vai CLI `--check-update`. Pārbaude neko neinstalē.

<!-- readme-unit:update.download -->
Lejupielāde sagatavo atbilstošo pārnēsājamo komplektu un pārbauda tā SHA256 pēc laidiena manifesta. GUI lejupielāde un `/update download` saglabā darbojošos instalāciju savā vietā.

<!-- readme-unit:update.install -->
Sagatavoto atjauninājumu instalējiet ar GUI instalēšanas pogu vai `/update install`. CLI `--update` skaidri pieprasa lejupielādi un instalēšanu. Esošais `supercli-data/` tiek saglabāts, bet aizstātajiem izpildāmajiem failiem izveido lokālas rezerves kopijas.

<!-- readme-unit:update.restart -->
Pēc instalēšanas restartējiet manuāli. Pirms instalēšanas aizveriet pārējās kopijas; Windows var bloķēt darbojošos izpildāmos failus. Ja nav atbilstoša komplekta vai pārbaudes metadatu, izmantojiet pārbaudītu manuālu laidiena lejupielādi. Saglabājiet savu datu rezerves kopiju.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentācija

<!-- readme-unit:docs.start -->
Sāciet ar [dokumentācijas rādītāju](../README.md), [ātro sākumu](../quickstart.md), [datu izkārtojumu](../data-layout.md) un [konfigurāciju](../configuration.md).

<!-- readme-unit:docs.engine -->
Īstenošanas detaļas skatiet dokumentos [arhitektūra](../architecture.md), [deleģēšana](../delegation.md), [veiktspēja](../performance.md), [GUI dizains](../webgui.md) un [projekta struktūra](../project-structure.md).

<!-- readme-unit:docs.extra -->
Iespēju ceļveži: [pārnēsājams MCP](../portable-mcp.md), [iebūvētās prasmes](../builtin-skills.md) un [telemetrija](../telemetry.md). Izstrādes pārraudzība: [plāns](../PLAN.md) un [ceļa karte](../ROADMAP.md).

<!-- readme-unit:reference -->
[Iepriekšējais pilnais README](../readme-reference.md) saglabāts kā vēsturiska atsauce; vecāki iespēju apraksti var būt pirms versijas `1.0.0`.

<!-- readme-unit:h.build -->
## Būvēšana un pārbaude

<!-- readme-unit:build -->
Izmantojiet `go.mod` norādīto Go versiju. Windows sistēmā `build.bat` būvē TUI, `build_ui.bat` GUI, bet `run.bat` vajadzības gadījumā būvē un palaiž termināli.

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
Pirms laidiena palaidiet Go testus. Dokumentācijas pārbaudītājs pārbauda visus 27 tulkojumus, sasaistīto saturu, virsrakstus, saites, tehniskos literāļus un identiskus koda piemērus.

<!-- readme-unit:requirements -->
Parastai lietošanai vajadzīgs konfigurēts modeļa galapunkts vai konts. Git un ripgrep uzlabo repozitoriju darbplūsmas; ārējie rīki ir izvēles, ja vien darbplūsmai tie nav vajadzīgi. GUI papildus prasa platformas webview/pārlūka atbalstu. Instalācijas diagnostikai izmantojiet `--doctor`.

<!-- readme-unit:h.license -->
## Licence

<!-- readme-unit:license -->
SuperCli izmanto [MIT licenci](../../LICENSE). Iekļautajām atkarībām un saturam ir savi paziņojumi; skatiet [trešo pušu paziņojumus](../../THIRD_PARTY_NOTICES.md) un [prasmju ceļvedi](../builtin-skills.md).
