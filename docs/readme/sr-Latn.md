[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.3

<!-- readme-unit:intro -->
Prenosivi AI agent za programiranje napisan u Go jeziku, sa terminalskim interfejsom (TUI), desktop/veb interfejsom (GUI) i paketnim režimom koji dele jedan mehanizam.

<!-- readme-unit:status -->
Ovaj README opisuje verziju `1.0.3`. Prenosivi paketi izdanja distribuiraju se preko [GitHub Releases](https://github.com/snakex21/SuperCli/releases); lokalno kompajliranje ne znači da je odgovarajuće izdanje već objavljeno.

<!-- readme-unit:h.screenshots -->
## Snimci ekrana

<!-- readme-unit:screenshot.gui -->
![Veb interfejs SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centar radnji u terminalu SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Slika GUI-ja je snimak ekrana; slika TUI-ja je prikaz stvarnog rasporeda terminalskog interfejsa aplikacije. [Još snimaka ekrana](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Početak rada

<!-- readme-unit:start -->
Raspakujte paket za svoj operativni sistem i arhitekturu u fasciklu u koju može da se piše. U Windowsu pokrenite `supercli.exe` za terminal ili `supercli-web.exe` za GUI. Priloženu fasciklu `supercli-data/` držite pored izvršnih datoteka.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Na Linuxu ili macOS-u koristite odgovarajuću izvršnu datoteku iz paketa ili kompajlirajte iz izvornog koda. Projekat izaberite pomoću `--home`; koristite `--batch` za jedan upit bez TUI-ja.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Prenosivi podaci

<!-- readme-unit:data -->
Podešavanja, sesije, memorija, akreditivi, keševi, dnevnici i rezervne kopije nalaze se u `supercli-data/` pored aplikacije. Premestite celu fasciklu aplikacije da ih ponesete sa sobom. Aplikacija za svoje stanje ne koristi `%APPDATA%`, `%LOCALAPPDATA%` niti Windows registar.

<!-- readme-unit:workspace -->
`--home` i `SUPERCLI_HOME` biraju radni prostor bez premeštanja podataka aplikacije. Projektna nadjačavanja podešavanja i radni artefakti koriste `<project>/.supercli/`.

<!-- readme-unit:override -->
Lokaciju podataka menja samo izričiti `--data-dir` ili `SUPERCLI_DATA_DIR`. Ako fascikla aplikacije nije upisiva, pokretanje prijavljuje grešku umesto neprimetnog korišćenja fascikle profila.

<!-- readme-unit:legacy -->
Terminal pri prvom pokretanju može da kopira stare podatke iz `~/.supercli` u prazan prenosivi direktorijum, zadržavajući original. Pogledajte [raspored podataka](../data-layout.md).

<!-- readme-unit:secrets -->
Akreditivi putuju s prenosivom fasciklom. Čuvajte njenu privatnost, pravite rezervne kopije i nikada ne unosite API ključeve ili datoteke za autentifikaciju u javni repozitorijum.

<!-- readme-unit:h.config -->
## Modeli i konfiguracija

<!-- readme-unit:providers -->
Podesite pružaoce kroz GUI podešavanja ili TUI `/providers` i `/models`. Podržane veze obuhvataju OpenAI-kompatibilne krajnje tačke, izvorni Anthropic, ChatGPT/Codex OAuth, opencode prolaze i offline echo pružaoca.

<!-- readme-unit:config -->
Globalna podešavanja su u `supercli-data/config.toml`; `<project>/.supercli/config.toml` može da ih nadjača. Promenljive okruženja i CLI opcije imaju prednost. Primer lokalne krajnje tačke:

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
Zamenite primer krajnje tačke i modela vrednostima svog servera. Usluge u oblaku mogu da zahtevaju akreditive i naplaćuju korišćenje. Mogućnosti modela određuju vid, alate, rezonovanje i ograničenja konteksta. Pogledajte [konfiguraciju](../configuration.md).

<!-- readme-unit:h.features -->
## Mogućnosti

<!-- readme-unit:surface -->
GUI i TUI podržavaju istih 27 jezika interfejsa. Pružaju razgovore u strimu, oporavak sesija, izbor projekata, upravljanje modelima, priloge i prikaze korišćenja. TUI podržava skrolovanje mišem, pretragu razgovora i sklopivi izlaz razmišljanja/alata.

<!-- readme-unit:agent -->
Mehanizam podržava otkrivanje alata, proverene izmene datoteka, izvršavanje komandi, istoriju sesija, memoriju projekta, sažimanje konteksta, ciljeve, delegiranje radnim agentima, konsultacije i opcione modele nacrta.

<!-- readme-unit:tools -->
Alati pokrivaju pretragu koda, ciljano čitanje i zakrpe datoteka, slike, ZIP arhive, DOCX/XLSX/PDF dokumente i ograničeno izvršavanje konteksta. Dostupni alati zavise od izabranog profila i modela; trenutni katalog potražite otkrivanjem alata.

<!-- readme-unit:extensions -->
Opcioni MCP paketi nalaze se u `supercli-data/mcp/` i pokreću se pri korišćenju. Arhiva ugrađenih veština je u `supercli-data/skills/builtin-skills.zip`; njen izostanak ne sprečava normalno pokretanje. Proširenja mogu zahtevati sopstvena izvršna okruženja ili instalirane aplikacije domaćina.

<!-- readme-unit:optional -->
Darwin generisanje kandidata, saveti modela i delegiranje radnim agentima opcioni su tokovi rada. Paralelni pozivi modela mogu povećati potrošnju resursa i troškove. Glavna izvršna datoteka ne zahteva Node, Python, Docker ili CGO.

<!-- readme-unit:h.controls -->
## Komande i upravljanje

<!-- readme-unit:controls -->
Unesite `/` za paletu komandi. TUI centar radnji otvorite sa `Tab` na praznom unosu ili `Ctrl+K`; strelicama birajte radnje i vratite se sa `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Komanda | Radnja |
| --- | --- |
| `/help`, `/doctor` | Prikaži pomoć i dijagnostiku. |
| `/model`, `/models`, `/providers` | Izaberi modele i upravljaj pružaocima. |
| `/resume`, `/export` | Nastavi ili izvezi razgovor. |
| `/status`, `/cost`, `/compact` | Pregledaj korišćenje ili sažmi kontekst. |
| `/goal`, `/plan` | Upravljaj ciljevima i režimom planiranja samo za čitanje. |
| `/diff`, `/undo`, `/redo` | Pregledaj izmene ili poništi/ponovi potez agenta. |
| `/darwin`, `/council` | Koristi opcione tokove s više modela. |
| `/update check\|download\|install` | Proveri, preuzmi ili izričito instaliraj ažuriranje. |
| `/quit`, `/exit` | Izađi iz aplikacije. |

<!-- readme-unit:keys -->
`Enter` šalje upit; `Ctrl+C` prekida. `PgUp`/`PgDn` i točkić miša skroluju; `Ctrl+F` pretražuje; `Shift+T` prebacuje razmišljanje; `Shift+E` proširuje izlaz alata. Upiti uneti tokom rada stavljaju se u red za sledeći bezbedan korak.

<!-- readme-unit:h.updates -->
## Ažuriranja

<!-- readme-unit:update.check -->
Stabilna GitHub izdanja proverite na zahtev iz GUI ekrana About, TUI komandom `/update check` ili CLI opcijom `--check-update`. Provera ništa ne instalira.

<!-- readme-unit:update.download -->
Preuzimanje priprema odgovarajući prenosivi paket i proverava njegov SHA256 prema manifestu izdanja. GUI preuzimanje i `/update download` ostavljaju pokrenutu instalaciju na mestu.

<!-- readme-unit:update.install -->
Pripremljeno ažuriranje instalirajte GUI dugmetom ili `/update install`. CLI `--update` izričito traži preuzimanje i instalaciju. Postojeći `supercli-data/` se čuva, a zamenjene izvršne datoteke dobijaju lokalne rezervne kopije.

<!-- readme-unit:update.restart -->
Nakon instalacije ponovo pokrenite ručno. Pre instalacije zatvorite druge kopije; izvršne datoteke u radu mogu biti zaključane u Windowsu. Ako nema odgovarajućeg paketa ili metapodataka provere, koristite provereno ručno preuzimanje izdanja. Čuvajte sopstvenu rezervnu kopiju podataka.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentacija

<!-- readme-unit:docs.start -->
Počnite od [indeksa dokumentacije](../README.md), [brzog početka](../quickstart.md), [rasporeda podataka](../data-layout.md) i [konfiguracije](../configuration.md).

<!-- readme-unit:docs.engine -->
Detalji implementacije: [arhitektura](../architecture.md), [delegiranje](../delegation.md), [performanse](../performance.md), [GUI dizajn](../webgui.md) i [struktura projekta](../project-structure.md).

<!-- readme-unit:docs.extra -->
Vodiči za funkcije: [prenosivi MCP](../portable-mcp.md), [ugrađene veštine](../builtin-skills.md) i [telemetrija](../telemetry.md). Praćenje razvoja: [plan](../PLAN.md) i [mapa razvoja](../ROADMAP.md).

<!-- readme-unit:reference -->
[Prethodni puni README](../readme-reference.md) zadržan je kao istorijska referenca; stariji opisi funkcija mogu prethoditi verziji `1.0.0`.

<!-- readme-unit:h.build -->
## Kompajliranje i provera

<!-- readme-unit:build -->
Koristite verziju Go navedenu u `go.mod`. U Windowsu `build.bat` kompajlira TUI, `build_ui.bat` GUI, a `run.bat` kompajlira po potrebi i pokreće terminal.

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
Pre izdanja pokrenite Go testove. Provera dokumentacije proverava svih 27 prevoda, povezani sadržaj, naslove, linkove, tehničke literale i identične primere koda.

<!-- readme-unit:requirements -->
Normalno korišćenje zahteva podešenu krajnju tačku modela ili nalog. Git i ripgrep poboljšavaju rad s repozitorijumima; spoljni alati su opcioni osim kada ih tok rada zahteva. GUI dodatno traži podršku platforme za webview/pregledač. Instalaciju dijagnostikujte pomoću `--doctor`.

<!-- readme-unit:h.license -->
## Licenca

<!-- readme-unit:license -->
SuperCli koristi [MIT licencu](../../LICENSE). Priložene zavisnosti i sadržaj imaju sopstvena obaveštenja; pogledajte [obaveštenja trećih strana](../../THIRD_PARTY_NOTICES.md) i [vodič za veštine](../builtin-skills.md).
