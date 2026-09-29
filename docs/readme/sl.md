[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Prenosni AI agent za programiranje, napisan v Go, s terminalskim vmesnikom (TUI), namiznim/spletnim vmesnikom (GUI) in paketnim načinom, ki uporabljajo isti pogon.

<!-- readme-unit:status -->
Ta README opisuje različico `1.0.0`. Prenosni paketi izdaj se razširjajo prek [GitHub Releases](https://github.com/snakex21/SuperCli/releases); lokalna gradnja ne pomeni, da je pripadajoča izdaja že objavljena.

<!-- readme-unit:h.start -->
## Začetek

<!-- readme-unit:start -->
Razširite paket za svoj operacijski sistem in arhitekturo v mapo z dovoljenjem za pisanje. V Windows za terminal zaženite `supercli.exe`, za GUI pa `supercli-web.exe`. Priloženo mapo `supercli-data/` obdržite ob izvršljivih datotekah.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
V Linux ali macOS uporabite ustrezno izvršljivo datoteko iz paketa ali zgradite iz izvorne kode. Projekt izberite z `--home`; za en poziv brez TUI uporabite `--batch`.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Prenosni podatki

<!-- readme-unit:data -->
Nastavitve, seje, pomnilnik, poverilnice, predpomnilniki, dnevniki in varnostne kopije so v `supercli-data/` ob aplikaciji. Premaknite celotno mapo aplikacije, da jih vzamete s seboj. Aplikacija svojega stanja ne shranjuje v `%APPDATA%`, `%LOCALAPPDATA%` ali register Windows.

<!-- readme-unit:workspace -->
`--home` in `SUPERCLI_HOME` izbereta delovni prostor brez premikanja podatkov aplikacije. Projektne preglasitve nastavitev in delovni artefakti uporabljajo `<project>/.supercli/`.

<!-- readme-unit:override -->
Mesto podatkov spremeni samo izrecni `--data-dir` ali `SUPERCLI_DATA_DIR`. Če mapa aplikacije ni zapisljiva, zagon javi napako, namesto da bi tiho uporabil mapo profila.

<!-- readme-unit:legacy -->
Terminal lahko ob prvem zagonu kopira stare podatke `~/.supercli` v prazen prenosni imenik in ohrani izvirnik. Glejte [razporeditev podatkov](../data-layout.md).

<!-- readme-unit:secrets -->
Poverilnice potujejo s prenosno mapo. Ohranite njeno zasebnost, izdelujte varnostne kopije in nikoli ne shranjujte API ključev ali avtentikacijskih datotek v javni repozitorij.

<!-- readme-unit:h.config -->
## Modeli in konfiguracija

<!-- readme-unit:providers -->
Ponudnike nastavite prek nastavitev GUI ali TUI `/providers` in `/models`. Podprte povezave vključujejo končne točke, združljive z OpenAI, izvorni Anthropic, ChatGPT/Codex OAuth, prehode opencode in ponudnika echo brez povezave.

<!-- readme-unit:config -->
Globalne nastavitve so v `supercli-data/config.toml`; `<project>/.supercli/config.toml` jih lahko preglasi. Okoljske spremenljivke in zastavice CLI imajo prednost. Primer lokalne končne točke:

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
Končno točko in model iz primera zamenjajte z vrednostmi svojega strežnika. Storitve v oblaku lahko zahtevajo poverilnice in zaračunavajo uporabo. Zmožnosti modela določajo vid, orodja, sklepanje in omejitve konteksta. Glejte [konfiguracijo](../configuration.md).

<!-- readme-unit:h.features -->
## Funkcije

<!-- readme-unit:surface -->
GUI in TUI podpirata istih 27 jezikov vmesnika. Zagotavljata pretočne pogovore, obnovitev sej, izbiro projektov, upravljanje modelov, priloge in prikaze uporabe. TUI podpira pomikanje z miško, iskanje po pogovoru in zložljiv izhod razmišljanja/orodij.

<!-- readme-unit:agent -->
Pogon podpira odkrivanje orodij, preverjene spremembe datotek, izvajanje ukazov, zgodovino sej, projektni pomnilnik, stiskanje konteksta, cilje, delegiranje delovnim agentom, posvetovanje in izbirne modele za osnutke.

<!-- readme-unit:tools -->
Orodja pokrivajo iskanje kode, ciljno branje in popravke datotek, slike, arhive ZIP, dokumente DOCX/XLSX/PDF in omejeno izvajanje konteksta. Razpoložljiva orodja so odvisna od izbranega profila in modela; trenutni katalog pridobite z odkrivanjem orodij.

<!-- readme-unit:extensions -->
Izbirni paketi MCP so v `supercli-data/mcp/` in se zaženejo ob uporabi. Arhiv vgrajenih veščin je v `supercli-data/skills/builtin-skills.zip`; njegova odsotnost ne prepreči običajnega zagona. Razširitve lahko potrebujejo lastna izvajalna okolja ali nameščene gostiteljske aplikacije.

<!-- readme-unit:optional -->
Ustvarjanje kandidatov Darwin, sveti modelov in delegiranje delovnim agentom so izbirni postopki. Vzporedni klici modelov lahko povečajo porabo virov in stroške. Osnovna izvršljiva datoteka ne potrebuje Node, Python, Docker ali CGO.

<!-- readme-unit:h.controls -->
## Ukazi in upravljanje

<!-- readme-unit:controls -->
Vnesite `/` za paleto ukazov. Center dejanj TUI odprete s `Tab` pri praznem vnosu ali `Ctrl+K`; dejanja izberite s puščicami in se vrnite z `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Ukaz | Dejanje |
| --- | --- |
| `/help`, `/doctor` | Prikaži pomoč in diagnostiko. |
| `/model`, `/models`, `/providers` | Izberi modele in upravljaj ponudnike. |
| `/resume`, `/export` | Nadaljuj ali izvozi pogovor. |
| `/status`, `/cost`, `/compact` | Preglej uporabo ali stisni kontekst. |
| `/goal`, `/plan` | Upravljaj cilje in način načrtovanja samo za branje. |
| `/diff`, `/undo`, `/redo` | Preglej spremembe ali razveljavi/ponovi korak agenta. |
| `/darwin`, `/council` | Uporabi izbirne postopke z več modeli. |
| `/update check\|download\|install` | Preveri, prenesi ali izrecno namesti posodobitev. |
| `/quit`, `/exit` | Zapri aplikacijo. |

<!-- readme-unit:keys -->
`Enter` pošlje poziv; `Ctrl+C` prekine. `PgUp`/`PgDn` in kolesce miške pomikajo; `Ctrl+F` išče; `Shift+T` preklopi razmišljanje; `Shift+E` razširi izhod orodij. Pozivi, vneseni med izvajanjem, se uvrstijo v vrsto za naslednji varen korak.

<!-- readme-unit:h.updates -->
## Posodobitve

<!-- readme-unit:update.check -->
Stabilne izdaje GitHub preverite na zahtevo na zaslonu About v GUI, s TUI `/update check` ali CLI `--check-update`. Preverjanje ne namesti ničesar.

<!-- readme-unit:update.download -->
Prenos pripravi ustrezen prenosni paket in preveri njegov SHA256 z manifestom izdaje. Prenos v GUI in `/update download` pustita trenutno namestitev na mestu.

<!-- readme-unit:update.install -->
Pripravljeno posodobitev namestite z gumbom GUI ali `/update install`. CLI `--update` izrecno zahteva prenos in namestitev. Obstoječi `supercli-data/` se ohrani, zamenjane izvršljive datoteke pa se lokalno varnostno kopirajo.

<!-- readme-unit:update.restart -->
Po namestitvi znova zaženite ročno. Pred namestitvijo zaprite druge kopije; Windows lahko zaklene izvršljive datoteke v uporabi. Če ustrezen paket ali metapodatki preverjanja niso na voljo, uporabite preverjen ročni prenos izdaje. Hranite lastno varnostno kopijo podatkov.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentacija

<!-- readme-unit:docs.start -->
Začnite z [kazalom dokumentacije](../README.md), [hitrim začetkom](../quickstart.md), [razporeditvijo podatkov](../data-layout.md) in [konfiguracijo](../configuration.md).

<!-- readme-unit:docs.engine -->
Podrobnosti izvedbe: [arhitektura](../architecture.md), [delegiranje](../delegation.md), [zmogljivost](../performance.md), [zasnova GUI](../webgui.md) in [struktura projekta](../project-structure.md).

<!-- readme-unit:docs.extra -->
Vodiči po funkcijah: [prenosni MCP](../portable-mcp.md), [vgrajene veščine](../builtin-skills.md) in [telemetrija](../telemetry.md). Spremljanje razvoja: [načrt](../PLAN.md) in [časovni načrt](../ROADMAP.md).

<!-- readme-unit:reference -->
[Prejšnji celotni README](../readme-reference.md) je ohranjen kot zgodovinska referenca; starejši opisi funkcij so lahko nastali pred različico `1.0.0`.

<!-- readme-unit:h.build -->
## Gradnja in preverjanje

<!-- readme-unit:build -->
Uporabite različico Go iz `go.mod`. V Windows `build.bat` zgradi TUI, `build_ui.bat` GUI, `run.bat` pa po potrebi zgradi in zažene terminal.

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
Pred izdajo zaženite teste Go. Preverjevalnik dokumentacije preverja vseh 27 prevodov, povezano vsebino, naslove, povezave, tehnične literale in enake primere kode.

<!-- readme-unit:requirements -->
Običajna uporaba potrebuje nastavljeno končno točko modela ali račun. Git in ripgrep izboljšata delo z repozitoriji; zunanja orodja so izbirna, razen če jih postopek zahteva. GUI dodatno potrebuje podporo platforme za webview/brskalnik. Za diagnostiko namestitve uporabite `--doctor`.

<!-- readme-unit:h.license -->
## Licenca

<!-- readme-unit:license -->
SuperCli uporablja [licenco MIT](../../LICENSE). Priložene odvisnosti in vsebina imajo lastna obvestila; glejte [obvestila tretjih oseb](../../THIRD_PARTY_NOTICES.md) in [vodič po veščinah](../builtin-skills.md).
