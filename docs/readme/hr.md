[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.3

<!-- readme-unit:intro -->
Prijenosni AI agent za programiranje napisan u Gou, s terminalskim sučeljem (TUI), stolnim/web sučeljem (GUI) i skupnim načinom rada koji dijele jedan pogon.

<!-- readme-unit:status -->
Ovaj README opisuje verziju `1.0.3`. Prijenosni paketi izdanja distribuiraju se putem [GitHub Releases](https://github.com/snakex21/SuperCli/releases); lokalna kompilacija ne znači da je pripadajuće izdanje već objavljeno.

<!-- readme-unit:h.screenshots -->
## Snimke zaslona

<!-- readme-unit:screenshot.gui -->
![Web-sučelje SuperClija (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centar radnji u terminalu SuperClija (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Slika GUI-ja je snimka zaslona; slika TUI-ja je prikaz stvarnog rasporeda terminalnog sučelja aplikacije. [Više snimki zaslona](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Početak rada

<!-- readme-unit:start -->
Raspakirajte paket za svoj operacijski sustav i arhitekturu u mapu s pravom pisanja. U Windowsu pokrenite `supercli.exe` za terminal ili `supercli-web.exe` za GUI. Priloženu mapu `supercli-data/` držite uz izvršne datoteke.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Na Linuxu ili macOS-u koristite odgovarajuću izvršnu datoteku iz paketa ili kompilirajte iz izvornog koda. Projekt odaberite pomoću `--home`; koristite `--batch` za jedan upit bez TUI-ja.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Prijenosni podaci

<!-- readme-unit:data -->
Postavke, sesije, memorija, vjerodajnice, predmemorije, zapisnici i sigurnosne kopije nalaze se u `supercli-data/` uz aplikaciju. Premjestite cijelu mapu aplikacije da ih ponesete sa sobom. Aplikacija za svoje stanje ne koristi `%APPDATA%`, `%LOCALAPPDATA%` ni registar Windowsa.

<!-- readme-unit:workspace -->
`--home` i `SUPERCLI_HOME` biraju radni prostor bez premještanja podataka aplikacije. Projektne nadjačavajuće postavke i radni artefakti koriste `<project>/.supercli/`.

<!-- readme-unit:override -->
Lokaciju podataka mijenja samo izričiti `--data-dir` ili `SUPERCLI_DATA_DIR`. Ako mapa aplikacije nije zapisiva, pokretanje prijavljuje pogrešku umjesto neprimjetnog korištenja mape profila.

<!-- readme-unit:legacy -->
Terminal pri prvom pokretanju može kopirati stare podatke iz `~/.supercli` u praznu prijenosnu mapu, zadržavajući izvornik. Pogledajte [raspored podataka](../data-layout.md).

<!-- readme-unit:secrets -->
Vjerodajnice putuju s prijenosnom mapom. Držite je privatnom, izrađujte sigurnosne kopije i nikad ne pohranjujte API ključeve ili autentifikacijske datoteke u javni repozitorij.

<!-- readme-unit:h.config -->
## Modeli i konfiguracija

<!-- readme-unit:providers -->
Pružatelje konfigurirajte u GUI postavkama ili TUI naredbama `/providers` i `/models`. Podržane veze uključuju OpenAI-kompatibilne krajnje točke, izvorni Anthropic, ChatGPT/Codex OAuth, opencode pristupnike i izvanmrežnog pružatelja echo.

<!-- readme-unit:config -->
Globalne postavke nalaze se u `supercli-data/config.toml`; `<project>/.supercli/config.toml` može ih nadjačati. Varijable okruženja i CLI opcije imaju prednost. Primjer lokalne krajnje točke:

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
Zamijenite primjer krajnje točke i modela vrijednostima svog poslužitelja. Usluge u oblaku mogu zahtijevati vjerodajnice i naplaćivati korištenje. Mogućnosti modela određuju vid, alate, zaključivanje i ograničenja konteksta. Pogledajte [konfiguraciju](../configuration.md).

<!-- readme-unit:h.features -->
## Mogućnosti

<!-- readme-unit:surface -->
GUI i TUI podržavaju istih 27 jezika sučelja. Nude strujanje razgovora, oporavak sesija, izbor projekata, upravljanje modelima, privitke i prikaze korištenja. TUI podržava pomicanje mišem, pretraživanje razgovora i sažimanje izlaza razmišljanja/alata.

<!-- readme-unit:agent -->
Pogon podržava otkrivanje alata, provjerene izmjene datoteka, izvršavanje naredbi, povijest sesija, memoriju projekta, sažimanje konteksta, ciljeve, delegiranje radnim agentima, konzultacije i neobvezne modele nacrta.

<!-- readme-unit:tools -->
Alati pokrivaju pretragu koda, ciljano čitanje i zakrpe datoteka, slike, ZIP arhive, DOCX/XLSX/PDF dokumente i ograničeno izvršavanje konteksta. Dostupni alati ovise o odabranom profilu i modelu; za trenutačni katalog koristite otkrivanje alata.

<!-- readme-unit:extensions -->
Neobvezni MCP paketi nalaze se u `supercli-data/mcp/` i pokreću se pri korištenju. Arhiva ugrađenih vještina nalazi se u `supercli-data/skills/builtin-skills.zip`; njezin izostanak ne sprječava normalno pokretanje. Proširenja mogu trebati vlastita izvršna okruženja ili instalirane aplikacije domaćina.

<!-- readme-unit:optional -->
Darwin generiranje kandidata, vijeća i delegiranje radnim agentima neobvezni su tijekovi rada. Paralelni pozivi modela mogu povećati potrošnju resursa i troškove. Osnovna izvršna datoteka ne zahtijeva Node, Python, Docker ni CGO.

<!-- readme-unit:h.controls -->
## Naredbe i upravljanje

<!-- readme-unit:controls -->
Upišite `/` za paletu naredbi. TUI centar radnji otvorite s `Tab` na praznom unosu ili `Ctrl+K`; strelicama birajte radnje, a s `Esc` se vratite.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Naredba | Radnja |
| --- | --- |
| `/help`, `/doctor` | Prikaz pomoći i dijagnostike. |
| `/model`, `/models`, `/providers` | Odabir modela i upravljanje pružateljima. |
| `/resume`, `/export` | Nastavak ili izvoz razgovora. |
| `/status`, `/cost`, `/compact` | Pregled korištenja ili sažimanje konteksta. |
| `/goal`, `/plan` | Upravljanje ciljevima i planiranjem samo za čitanje. |
| `/diff`, `/undo`, `/redo` | Pregled izmjena ili poništavanje/ponavljanje poteza agenta. |
| `/darwin`, `/council` | Korištenje neobveznih tijekova s više modela. |
| `/update check\|download\|install` | Provjera, preuzimanje ili izričita instalacija ažuriranja. |
| `/quit`, `/exit` | Izlaz iz aplikacije. |

<!-- readme-unit:keys -->
`Enter` šalje upit; `Ctrl+C` prekida. `PgUp`/`PgDn` i kotačić miša pomiču; `Ctrl+F` pretražuje; `Shift+T` uključuje/isključuje razmišljanje; `Shift+E` proširuje izlaz alata. Upiti uneseni tijekom rada stavljaju se u red za sljedeći siguran korak.

<!-- readme-unit:h.updates -->
## Ažuriranja

<!-- readme-unit:update.check -->
Stabilna GitHub izdanja provjerite na zahtjev s GUI zaslona About, TUI naredbom `/update check` ili CLI opcijom `--check-update`. Provjera ništa ne instalira.

<!-- readme-unit:update.download -->
Preuzimanje priprema odgovarajući prijenosni paket i provjerava njegov SHA256 prema manifestu izdanja. GUI preuzimanje i `/update download` ostavljaju trenutačnu instalaciju na mjestu.

<!-- readme-unit:update.install -->
Pripremljeno ažuriranje instalirajte GUI gumbom za instalaciju ili naredbom `/update install`. CLI `--update` izričito traži preuzimanje i instalaciju. Postojeći `supercli-data/` čuva se, a zamijenjene izvršne datoteke lokalno se sigurnosno kopiraju.

<!-- readme-unit:update.restart -->
Nakon instalacije ručno ponovno pokrenite aplikaciju. Prije instalacije zatvorite druge kopije; Windows može zaključati pokrenute izvršne datoteke. Ako odgovarajući paket ili metapodaci provjere nisu dostupni, koristite provjereno ručno preuzimanje izdanja. Čuvajte vlastitu sigurnosnu kopiju podataka.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentacija

<!-- readme-unit:docs.start -->
Počnite s [kazalom dokumentacije](../README.md), [brzim početkom](../quickstart.md), [rasporedom podataka](../data-layout.md) i [konfiguracijom](../configuration.md).

<!-- readme-unit:docs.engine -->
Za detalje implementacije pročitajte [arhitekturu](../architecture.md), [delegiranje](../delegation.md), [performanse](../performance.md), [GUI dizajn](../webgui.md) i [strukturu projekta](../project-structure.md).

<!-- readme-unit:docs.extra -->
Vodiči za značajke: [prijenosni MCP](../portable-mcp.md), [ugrađene vještine](../builtin-skills.md) i [telemetrija](../telemetry.md). Praćenje razvoja: [plan](../PLAN.md) i [plan razvoja](../ROADMAP.md).

<!-- readme-unit:reference -->
[Prethodni puni README](../readme-reference.md) zadržan je kao povijesna referenca; stariji opisi značajki mogu prethoditi verziji `1.0.0`.

<!-- readme-unit:h.build -->
## Kompiliranje i provjera

<!-- readme-unit:build -->
Koristite verziju Go navedenu u `go.mod`. U Windowsu `build.bat` kompilira TUI, `build_ui.bat` GUI, a `run.bat` kompilira po potrebi i pokreće terminal.

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
Prije izdanja pokrenite Go testove. Provjera dokumentacije provjerava svih 27 prijevoda, povezani sadržaj, naslove, poveznice, tehničke literale i identične primjere koda.

<!-- readme-unit:requirements -->
Normalno korištenje zahtijeva konfiguriranu krajnju točku modela ili račun. Git i ripgrep poboljšavaju rad s repozitorijima; vanjski alati nisu obvezni osim ako ih tijek rada traži. GUI dodatno zahtijeva podršku platforme za webview/preglednik. Za dijagnostiku instalacije koristite `--doctor`.

<!-- readme-unit:h.license -->
## Licenca

<!-- readme-unit:license -->
SuperCli koristi [MIT licencu](../../LICENSE). Priložene ovisnosti i sadržaj imaju vlastite obavijesti; pogledajte [obavijesti trećih strana](../../THIRD_PARTY_NOTICES.md) i [vodič za vještine](../builtin-skills.md).
