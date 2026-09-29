[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Go kalba parašytas nešiojamasis AI programavimo agentas, kurio terminalo sąsaja (TUI), darbalaukio/žiniatinklio sąsaja (GUI) ir paketinis režimas naudoja vieną variklį.

<!-- readme-unit:status -->
Šis README aprašo versiją `1.0.0`. Nešiojamieji leidimų paketai platinami per [GitHub Releases](https://github.com/snakex21/SuperCli/releases); vietinis sukompiliavimas nereiškia, kad jo leidimas jau paskelbtas.

<!-- readme-unit:h.screenshots -->
## Ekrano nuotraukos

<!-- readme-unit:screenshot.gui -->
![SuperCli žiniatinklio sąsaja (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperCli terminalo veiksmų centras (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
GUI vaizdas yra ekrano nuotrauka; TUI vaizdas yra atvaizduotas tikrasis programos terminalo išdėstymas. [Daugiau ekrano nuotraukų](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Darbo pradžia

<!-- readme-unit:start -->
Išskleiskite savo operacinei sistemai ir architektūrai skirtą paketą į aplanką, kuriame galima rašyti. Windows sistemoje terminalui paleiskite `supercli.exe`, GUI – `supercli-web.exe`. Pridėtą `supercli-data/` aplanką laikykite šalia vykdomųjų failų.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Linux arba macOS sistemoje naudokite atitinkamą paketo vykdomąjį failą arba kompiliuokite iš šaltinio kodo. Projektą pasirinkite su `--home`; vienai užklausai be TUI naudokite `--batch`.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Nešiojamieji duomenys

<!-- readme-unit:data -->
Nustatymai, sesijos, atmintis, prisijungimo duomenys, podėliai, žurnalai ir atsarginės kopijos saugomi `supercli-data/` šalia programos. Perkelkite visą programos aplanką, kad perkeltumėte ir juos. Programa savo būsenai nenaudoja `%APPDATA%`, `%LOCALAPPDATA%` ar Windows registro.

<!-- readme-unit:workspace -->
`--home` ir `SUPERCLI_HOME` pasirenka darbo sritį neperkeldami programos duomenų. Projekto nustatymų pakeitimai ir darbo srities failai naudoja `<project>/.supercli/`.

<!-- readme-unit:override -->
Duomenų vietą pakeičia tik aiškiai nurodytas `--data-dir` arba `SUPERCLI_DATA_DIR`. Jei į programos aplanką negalima rašyti, paleidimas praneša klaidą, o ne tyliai naudoja profilio aplanką.

<!-- readme-unit:legacy -->
Pirmą kartą paleistas terminalas gali nukopijuoti senuosius `~/.supercli` duomenis į tuščią nešiojamąjį katalogą, išsaugodamas originalą. Žr. [duomenų struktūrą](../data-layout.md).

<!-- readme-unit:secrets -->
Prisijungimo duomenys keliauja kartu su nešiojamuoju aplanku. Laikykite jį privatų, kurkite atsargines kopijas ir niekada neįtraukite API raktų ar autentifikavimo failų į viešą repozitoriją.

<!-- readme-unit:h.config -->
## Modeliai ir konfigūracija

<!-- readme-unit:providers -->
Teikėjus konfigūruokite GUI nustatymuose arba TUI `/providers` ir `/models`. Palaikomi su OpenAI suderinami galiniai taškai, vietinis Anthropic, ChatGPT/Codex OAuth, opencode šliuzai ir neprisijungęs echo teikėjas.

<!-- readme-unit:config -->
Bendrieji nustatymai yra `supercli-data/config.toml`; `<project>/.supercli/config.toml` gali juos pakeisti. Aplinkos kintamieji ir CLI parinktys turi pirmenybę. Vietinio galinio taško pavyzdys:

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
Pavyzdžio galinį tašką ir modelį pakeiskite savo serverio reikšmėmis. Debesijos paslaugos gali reikalauti prisijungimo duomenų ir apmokestinti naudojimą. Modelio galimybės lemia vaizdų supratimą, įrankius, samprotavimą ir konteksto ribas. Žr. [konfigūraciją](../configuration.md).

<!-- readme-unit:h.features -->
## Funkcijos

<!-- readme-unit:surface -->
GUI ir TUI palaiko tas pačias 27 sąsajos kalbas. Jos teikia srautinius pokalbius, sesijų atkūrimą, projektų pasirinkimą, modelių valdymą, priedus ir naudojimo rodinius. TUI palaiko slinkimą pele, paiešką pokalbyje ir sutraukiamą mąstymo/įrankių išvestį.

<!-- readme-unit:agent -->
Variklis palaiko įrankių paiešką, patikrintus failų redagavimus, komandų vykdymą, sesijų istoriją, projekto atmintį, konteksto glaudinimą, tikslus, delegavimą darbo agentams, konsultacijas ir pasirenkamus juodraščių modelius.

<!-- readme-unit:tools -->
Įrankiai apima kodo paiešką, tikslinius failų skaitymus ir pataisas, vaizdus, ZIP archyvus, DOCX/XLSX/PDF dokumentus ir ribotą konteksto vykdymą. Prieinami įrankiai priklauso nuo pasirinkto profilio ir modelio; dabartinį katalogą raskite įrankių paieška.

<!-- readme-unit:extensions -->
Pasirenkami MCP paketai yra `supercli-data/mcp/` ir paleidžiami juos naudojant. Integruotų įgūdžių archyvas yra `supercli-data/skills/builtin-skills.zip`; jo nebuvimas netrukdo įprastai paleisti programą. Plėtiniams gali reikėti savų vykdymo aplinkų ar įdiegtų pagrindinių programų.

<!-- readme-unit:optional -->
Darwin kandidatų generavimas, modelių tarybos ir delegavimas darbo agentams yra pasirenkami procesai. Lygiagretūs modelių kvietimai gali padidinti išteklių naudojimą ir kainą. Pagrindinis vykdomasis failas nereikalauja Node, Python, Docker ar CGO.

<!-- readme-unit:h.controls -->
## Komandos ir valdymas

<!-- readme-unit:controls -->
Komandų paletei įveskite `/`. TUI veiksmų centrą atidarykite su `Tab` tuščiame įvedimo lauke arba `Ctrl+K`; veiksmus rinkitės rodyklėmis, grįžkite su `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Komanda | Veiksmas |
| --- | --- |
| `/help`, `/doctor` | Rodyti pagalbą ir diagnostiką. |
| `/model`, `/models`, `/providers` | Pasirinkti modelius ir valdyti teikėjus. |
| `/resume`, `/export` | Tęsti arba eksportuoti pokalbį. |
| `/status`, `/cost`, `/compact` | Peržiūrėti naudojimą arba glaudinti kontekstą. |
| `/goal`, `/plan` | Valdyti tikslus ir tik skaitymo planavimo režimą. |
| `/diff`, `/undo`, `/redo` | Peržiūrėti pakeitimus arba atšaukti/pakartoti agento žingsnį. |
| `/darwin`, `/council` | Naudoti pasirenkamus kelių modelių procesus. |
| `/update check\|download\|install` | Tikrinti, atsisiųsti arba aiškiai įdiegti naujinį. |
| `/quit`, `/exit` | Išeiti iš programos. |

<!-- readme-unit:keys -->
`Enter` išsiunčia užklausą; `Ctrl+C` nutraukia. `PgUp`/`PgDn` ir pelės ratukas slenka; `Ctrl+F` ieško; `Shift+T` perjungia mąstymą; `Shift+E` išplečia įrankių išvestį. Vykdymo metu įvestos užklausos rikiuojamos kitam saugiam žingsniui.

<!-- readme-unit:h.updates -->
## Naujinimai

<!-- readme-unit:update.check -->
Stabilius GitHub leidimus pagal poreikį tikrinkite GUI About ekrane, TUI `/update check` arba CLI `--check-update`. Tikrinimas nieko neįdiegia.

<!-- readme-unit:update.download -->
Atsisiuntimas paruošia atitinkamą nešiojamąjį paketą ir patikrina jo SHA256 pagal leidimo manifestą. GUI atsisiuntimas ir `/update download` palieka veikiančią įdiegtą versiją vietoje.

<!-- readme-unit:update.install -->
Paruoštą naujinį įdiekite GUI diegimo mygtuku arba `/update install`. CLI `--update` aiškiai prašo atsisiuntimo ir diegimo. Esamas `supercli-data/` išsaugomas, o pakeičiami vykdomieji failai atsarginėmis kopijomis saugomi vietoje.

<!-- readme-unit:update.restart -->
Po diegimo paleiskite iš naujo rankiniu būdu. Prieš diegdami uždarykite kitas kopijas; Windows gali užrakinti veikiančius vykdomuosius failus. Jei nėra tinkamo paketo ar patikros metaduomenų, naudokite patikrintą rankinį leidimo atsisiuntimą. Laikykite savo duomenų atsarginę kopiją.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentacija

<!-- readme-unit:docs.start -->
Pradėkite nuo [dokumentacijos rodyklės](../README.md), [greitosios pradžios](../quickstart.md), [duomenų struktūros](../data-layout.md) ir [konfigūracijos](../configuration.md).

<!-- readme-unit:docs.engine -->
Įgyvendinimo informaciją rasite dokumentuose [architektūra](../architecture.md), [delegavimas](../delegation.md), [našumas](../performance.md), [GUI dizainas](../webgui.md) ir [projekto struktūra](../project-structure.md).

<!-- readme-unit:docs.extra -->
Funkcijų vadovai: [nešiojamasis MCP](../portable-mcp.md), [integruoti įgūdžiai](../builtin-skills.md) ir [telemetrija](../telemetry.md). Plėtros stebėjimas: [planas](../PLAN.md) ir [gairės](../ROADMAP.md).

<!-- readme-unit:reference -->
[Ankstesnis visas README](../readme-reference.md) paliktas kaip istorinė nuoroda; senesni funkcijų aprašymai gali būti ankstesni nei versija `1.0.0`.

<!-- readme-unit:h.build -->
## Kompiliavimas ir tikrinimas

<!-- readme-unit:build -->
Naudokite `go.mod` nurodytą Go versiją. Windows sistemoje `build.bat` sukompiliuoja TUI, `build_ui.bat` GUI, o `run.bat` prireikus kompiliuoja ir paleidžia terminalą.

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
Prieš leidimą paleiskite Go testus. Dokumentacijos tikrintuvas tikrina visus 27 vertimus, susietą turinį, antraštes, nuorodas, techninius literalus ir identiškus kodo pavyzdžius.

<!-- readme-unit:requirements -->
Įprastam naudojimui reikia sukonfigūruoto modelio galinio taško arba paskyros. Git ir ripgrep pagerina darbą su repozitorijomis; išoriniai įrankiai pasirenkami, nebent procesui jų reikia. GUI papildomai reikia platformos webview/naršyklės palaikymo. Diegimą diagnozuokite su `--doctor`.

<!-- readme-unit:h.license -->
## Licencija

<!-- readme-unit:license -->
SuperCli naudoja [MIT licenciją](../../LICENSE). Pridėtos priklausomybės ir turinys turi savus pranešimus; žr. [trečiųjų šalių pranešimus](../../THIRD_PARTY_NOTICES.md) ir [įgūdžių vadovą](../builtin-skills.md).
