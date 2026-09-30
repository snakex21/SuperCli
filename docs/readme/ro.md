[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.1

<!-- readme-unit:intro -->
Un agent AI portabil pentru programare, scris în Go, cu interfață de terminal (TUI), interfață desktop/web (GUI) și mod batch care folosesc același motor.

<!-- readme-unit:status -->
Acest README descrie versiunea `1.0.1`. Pachetele portabile sunt distribuite prin [GitHub Releases](https://github.com/snakex21/SuperCli/releases); o compilare locală nu înseamnă că versiunea ei a fost deja publicată.

<!-- readme-unit:h.screenshots -->
## Capturi de ecran

<!-- readme-unit:screenshot.gui -->
![Interfața web SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centrul de acțiuni din terminalul SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Imaginea GUI este o captură de ecran; imaginea TUI este o redare a structurii reale a interfeței de terminal a aplicației. [Mai multe capturi de ecran](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Primii pași

<!-- readme-unit:start -->
Extrageți pachetul pentru sistemul de operare și arhitectura dvs. într-un folder cu drept de scriere. În Windows, porniți `supercli.exe` pentru terminal sau `supercli-web.exe` pentru GUI. Păstrați folderul inclus `supercli-data/` lângă executabile.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
În Linux sau macOS, folosiți executabilul corespunzător din pachet sau compilați sursele. Selectați proiectul cu `--home`; folosiți `--batch` pentru o singură cerere fără TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Date portabile

<!-- readme-unit:data -->
Setările, sesiunile, memoria, acreditările, cache-urile, jurnalele și copiile de siguranță se află în `supercli-data/` lângă aplicație. Mutați întregul folder al aplicației pentru a le lua cu dvs. Aplicația nu folosește `%APPDATA%`, `%LOCALAPPDATA%` sau registrul Windows pentru starea sa.

<!-- readme-unit:workspace -->
`--home` și `SUPERCLI_HOME` selectează spațiul de lucru fără a muta datele aplicației. Setările suprascrise ale proiectului și fișierele de lucru folosesc `<project>/.supercli/`.

<!-- readme-unit:override -->
Doar un `--data-dir` sau `SUPERCLI_DATA_DIR` explicit schimbă locația datelor. Dacă folderul aplicației nu permite scrierea, pornirea raportează o eroare în loc să folosească în tăcere un director de profil.

<!-- readme-unit:legacy -->
La prima pornire, terminalul poate copia datele vechi din `~/.supercli` într-un director portabil gol, păstrând originalul. Consultați [organizarea datelor](../data-layout.md).

<!-- readme-unit:secrets -->
Acreditările călătoresc cu folderul portabil. Păstrați-l privat, faceți copii de siguranță și nu înregistrați niciodată chei API sau fișiere de autentificare într-un depozit public.

<!-- readme-unit:h.config -->
## Modele și configurare

<!-- readme-unit:providers -->
Configurați furnizorii din setările GUI sau prin TUI `/providers` și `/models`. Conexiunile acceptate includ endpointuri compatibile OpenAI, Anthropic nativ, ChatGPT/Codex OAuth, gateway-uri opencode și un furnizor echo offline.

<!-- readme-unit:config -->
Setările globale sunt în `supercli-data/config.toml`; `<project>/.supercli/config.toml` le poate suprascrie. Variabilele de mediu și opțiunile CLI au prioritate. Exemplu de endpoint local:

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
Înlocuiți endpointul și modelul din exemplu cu valorile serverului dvs. Serviciile cloud pot cere acreditări și taxa utilizarea. Capacitățile modelului determină vederea, instrumentele, raționamentul și limitele contextului. Consultați [configurarea](../configuration.md).

<!-- readme-unit:h.features -->
## Funcționalități

<!-- readme-unit:surface -->
GUI și TUI acceptă aceleași 27 de limbi de interfață. Oferă conversații în flux, recuperarea sesiunilor, alegerea proiectelor, gestionarea modelelor, atașamente și vizualizări ale utilizării. TUI permite derularea cu mouse-ul, căutarea în conversație și restrângerea ieșirilor de raționament/instrumente.

<!-- readme-unit:agent -->
Motorul permite descoperirea instrumentelor, editări verificate de fișiere, executarea comenzilor, istoricul sesiunilor, memoria proiectului, compactarea contextului, obiective, delegare către agenți de lucru, consultare și modele opționale de schiță.

<!-- readme-unit:tools -->
Instrumentele acoperă căutarea în cod, citiri și patch-uri țintite, imagini, arhive ZIP, documente DOCX/XLSX/PDF și execuție de context limitată. Instrumentele disponibile depind de profilul și modelul ales; folosiți descoperirea instrumentelor pentru catalogul curent.

<!-- readme-unit:extensions -->
Pachetele MCP opționale sunt în `supercli-data/mcp/` și pornesc când sunt folosite. Arhiva competențelor integrate este în `supercli-data/skills/builtin-skills.zip`; absența ei nu împiedică pornirea normală. Extensiile pot necesita propriile medii de execuție sau aplicații gazdă instalate.

<!-- readme-unit:optional -->
Generarea candidaților Darwin, consiliile și delegarea către agenți de lucru sunt fluxuri opționale. Apelurile paralele către modele pot crește consumul de resurse și costul. Executabilul principal nu necesită Node, Python, Docker sau CGO.

<!-- readme-unit:h.controls -->
## Comenzi și controale

<!-- readme-unit:controls -->
Tastați `/` pentru paleta de comenzi. Deschideți centrul de acțiuni TUI cu `Tab` când intrarea este goală sau `Ctrl+K`; selectați acțiuni cu săgețile și reveniți cu `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Comandă | Acțiune |
| --- | --- |
| `/help`, `/doctor` | Afișați ajutorul și diagnosticele. |
| `/model`, `/models`, `/providers` | Alegeți modele și gestionați furnizorii. |
| `/resume`, `/export` | Reluați sau exportați o conversație. |
| `/status`, `/cost`, `/compact` | Inspectați utilizarea sau compactați contextul. |
| `/goal`, `/plan` | Gestionați obiectivele și modul de planificare doar pentru citire. |
| `/diff`, `/undo`, `/redo` | Inspectați modificările sau anulați/refaceți o tură a agentului. |
| `/darwin`, `/council` | Folosiți fluxuri opționale cu mai multe modele. |
| `/update check\|download\|install` | Verificați, descărcați sau instalați explicit o actualizare. |
| `/quit`, `/exit` | Ieșiți din aplicație. |

<!-- readme-unit:keys -->
`Enter` trimite o cerere; `Ctrl+C` întrerupe. `PgUp`/`PgDn` și rotița mouse-ului derulează; `Ctrl+F` caută; `Shift+T` comută raționamentul; `Shift+E` extinde ieșirea instrumentelor. Cererile introduse în timpul execuției sunt puse în coadă pentru următorul pas sigur.

<!-- readme-unit:h.updates -->
## Actualizări

<!-- readme-unit:update.check -->
Verificați versiunile stabile GitHub la cerere din ecranul About al GUI, TUI `/update check` sau CLI `--check-update`. Verificarea nu instalează nimic.

<!-- readme-unit:update.download -->
Descărcarea pregătește pachetul portabil corespunzător și verifică SHA256 față de manifestul versiunii. Descărcarea GUI și `/update download` lasă instalarea în execuție la locul ei.

<!-- readme-unit:update.install -->
Folosiți butonul de instalare GUI sau `/update install` pentru actualizarea pregătită. CLI `--update` solicită explicit descărcare și instalare. `supercli-data/` existent se păstrează, iar executabilele înlocuite sunt salvate în copii locale.

<!-- readme-unit:update.restart -->
Reporniți manual după instalare. Închideți celelalte copii înainte de instalare; executabilele în uz pot fi blocate în Windows. Dacă lipsesc pachetul potrivit sau metadatele de verificare, folosiți o descărcare manuală verificată a versiunii. Păstrați propria copie de siguranță a datelor.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Documentație

<!-- readme-unit:docs.start -->
Începeți cu [indexul documentației](../README.md), [pornirea rapidă](../quickstart.md), [organizarea datelor](../data-layout.md) și [configurarea](../configuration.md).

<!-- readme-unit:docs.engine -->
Citiți [arhitectura](../architecture.md), [delegarea](../delegation.md), [performanța](../performance.md), [designul GUI](../webgui.md) și [structura proiectului](../project-structure.md) pentru detalii de implementare.

<!-- readme-unit:docs.extra -->
Ghiduri de funcții: [MCP portabil](../portable-mcp.md), [competențe integrate](../builtin-skills.md) și [telemetrie](../telemetry.md). Urmărirea dezvoltării: [plan](../PLAN.md) și [foaie de parcurs](../ROADMAP.md).

<!-- readme-unit:reference -->
[README-ul complet anterior](../readme-reference.md) este păstrat ca referință istorică; descrierile mai vechi pot preceda versiunea `1.0.0`.

<!-- readme-unit:h.build -->
## Compilare și verificare

<!-- readme-unit:build -->
Folosiți versiunea Go specificată în `go.mod`. În Windows, `build.bat` compilează TUI, `build_ui.bat` GUI, iar `run.bat` compilează dacă este necesar și pornește terminalul.

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
Rulați testele Go înaintea unei versiuni. Verificatorul documentației verifică toate cele 27 de traduceri, conținutul asociat, titlurile, linkurile, literalii tehnici și exemplele de cod identice.

<!-- readme-unit:requirements -->
Utilizarea normală necesită un endpoint de model configurat sau un cont. Git și ripgrep îmbunătățesc lucrul cu depozite; instrumentele externe sunt opționale dacă fluxul nu le cere. GUI necesită suplimentar suportul webview/browser al platformei. Folosiți `--doctor` pentru diagnosticul instalării.

<!-- readme-unit:h.license -->
## Licență

<!-- readme-unit:license -->
SuperCli folosește [licența MIT](../../LICENSE). Dependențele și conținutul incluse au propriile notificări; consultați [notificările terților](../../THIRD_PARTY_NOTICES.md) și [ghidul competențelor](../builtin-skills.md).
