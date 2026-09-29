[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Go-kielellä kirjoitettu siirrettävä tekoälykoodausagentti, jonka päätekäyttöliittymä (TUI), työpöytä-/verkkokäyttöliittymä (GUI) ja eräajotila jakavat saman moottorin.

<!-- readme-unit:status -->
Tämä README kuvaa versiota `1.0.0`. Siirrettävät julkaisupaketit jaetaan [GitHub Releases](https://github.com/snakex21/SuperCli/releases) -palvelussa; paikallinen koonti ei tarkoita, että sen julkaisu olisi jo saatavilla.

<!-- readme-unit:h.screenshots -->
## Kuvakaappaukset

<!-- readme-unit:screenshot.gui -->
![SuperClin verkkokäyttöliittymä (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperClin päätteen toimintokeskus (TUI)](../screenshots/1.0.0/tui-actions-pl.jpg)

<!-- readme-unit:screenshots.more -->
GUI-kuva on kuvakaappaus; TUI-kuva on sovelluksen todellisen päätenäkymän renderöinti. [Lisää kuvakaappauksia](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Aloittaminen

<!-- readme-unit:start -->
Pura käyttöjärjestelmääsi ja arkkitehtuuriisi sopiva paketti kirjoitettavaan kansioon. Käynnistä Windowsissa `supercli.exe` päätettä tai `supercli-web.exe` GUI:ta varten. Pidä mukana toimitettu `supercli-data/`-kansio suoritettavien tiedostojen vieressä.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Käytä Linuxissa tai macOS:ssä paketin vastaavaa suoritettavaa tiedostoa tai käännä lähdekoodista. Valitse projekti valitsimella `--home`; käytä `--batch`-valitsinta yhteen pyyntöön ilman TUI:ta.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Siirrettävät tiedot

<!-- readme-unit:data -->
Asetukset, istunnot, muisti, tunnistetiedot, välimuistit, lokit ja varmuuskopiot sijaitsevat sovelluksen vieressä kansiossa `supercli-data/`. Siirrä koko sovelluskansio ottaaksesi ne mukaan. Sovellus ei tallenna tilaansa sijainteihin `%APPDATA%`, `%LOCALAPPDATA%` eikä Windowsin rekisteriin.

<!-- readme-unit:workspace -->
`--home` ja `SUPERCLI_HOME` valitsevat työtilan siirtämättä sovelluksen tietoja. Projektin ylikirjoittavat asetukset ja työtilan tiedostot käyttävät sijaintia `<project>/.supercli/`.

<!-- readme-unit:override -->
Vain erikseen asetettu `--data-dir` tai `SUPERCLI_DATA_DIR` muuttaa tietojen sijaintia. Jos sovelluskansioon ei voi kirjoittaa, käynnistys ilmoittaa virheen eikä siirry huomaamatta profiilikansioon.

<!-- readme-unit:legacy -->
Pääte voi ensimmäisellä käynnistyksellä kopioida vanhat `~/.supercli`-tiedot tyhjään siirrettävään hakemistoon säilyttäen alkuperäiset. Katso [tietojen rakenne](../data-layout.md).

<!-- readme-unit:secrets -->
Tunnistetiedot kulkevat siirrettävän kansion mukana. Pidä kansio yksityisenä, varmuuskopioi se äläkä koskaan tallenna API-avaimia tai tunnistautumistiedostoja julkiseen tietovarastoon.

<!-- readme-unit:h.config -->
## Mallit ja asetukset

<!-- readme-unit:providers -->
Määritä palveluntarjoajat GUI:n asetuksissa tai TUI:n `/providers`- ja `/models`-komennoilla. Tuettuja yhteyksiä ovat OpenAI-yhteensopivat päätepisteet, natiivi Anthropic, ChatGPT/Codex OAuth, opencode-yhdyskäytävät ja yhteydetön echo-palveluntarjoaja.

<!-- readme-unit:config -->
Yleiset asetukset ovat tiedostossa `supercli-data/config.toml`; `<project>/.supercli/config.toml` voi ohittaa ne. Ympäristömuuttujat ja CLI-valitsimet ovat etusijalla. Esimerkki paikallisesta päätepisteestä:

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
Korvaa esimerkin päätepiste ja malli palvelimesi arvoilla. Pilvipalvelut voivat vaatia tunnistetietoja ja veloittaa käytöstä. Mallin ominaisuudet määrittävät kuvantunnistuksen, työkalut, päättelyn ja kontekstirajat. Katso [asetukset](../configuration.md).

<!-- readme-unit:h.features -->
## Ominaisuudet

<!-- readme-unit:surface -->
GUI ja TUI tukevat samoja 27 käyttöliittymäkieltä. Ne tarjoavat suoratoistetut keskustelut, istuntojen palautuksen, projektivalinnan, mallinhallinnan, liitteet ja käyttönäkymät. TUI tukee hiirivieritystä, keskusteluhakua ja kutistettavaa päättely-/työkalutulostetta.

<!-- readme-unit:agent -->
Moottori tukee työkalujen löytämistä, tarkistettuja tiedostomuutoksia, komentojen suorittamista, istuntohistoriaa, projektimuistia, kontekstin tiivistämistä, tavoitteita, delegointia työagenteille, konsultointia ja valinnaisia luonnosmalleja.

<!-- readme-unit:tools -->
Työkalut kattavat koodihaun, kohdistetut tiedostoluvut ja paikkaukset, kuvat, ZIP-arkistot, DOCX/XLSX/PDF-asiakirjat ja rajatun kontekstisuorituksen. Saatavilla olevat työkalut riippuvat valitusta profiilista ja mallista; löydä nykyinen luettelo työkaluhakutoiminnolla.

<!-- readme-unit:extensions -->
Valinnaiset MCP-paketit sijaitsevat kansiossa `supercli-data/mcp/` ja käynnistyvät käytettäessä. Sisäänrakennettujen taitojen arkisto on `supercli-data/skills/builtin-skills.zip`; sen puuttuminen ei estä normaalia käynnistystä. Laajennukset voivat tarvita omia suoritusympäristöjä tai asennettuja isäntäsovelluksia.

<!-- readme-unit:optional -->
Darwin-ehdokkaiden luonti, mallineuvostot ja delegointi työagenteille ovat valinnaisia työnkulkuja. Rinnakkaiset mallikutsut voivat kasvattaa resurssikulutusta ja kustannuksia. Ydinohjelma ei vaadi Nodea, Pythonia, Dockeria tai CGO:ta.

<!-- readme-unit:h.controls -->
## Komennot ja ohjaus

<!-- readme-unit:controls -->
Avaa komentopaletti kirjoittamalla `/`. Avaa TUI:n toimintokeskus tyhjässä syötteessä näppäimellä `Tab` tai `Ctrl+K`; valitse toiminnot nuolilla ja palaa näppäimellä `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Komento | Toiminto |
| --- | --- |
| `/help`, `/doctor` | Näytä ohje ja diagnostiikka. |
| `/model`, `/models`, `/providers` | Valitse mallit ja hallitse palveluntarjoajia. |
| `/resume`, `/export` | Jatka keskustelua tai vie se tiedostoon. |
| `/status`, `/cost`, `/compact` | Tarkastele käyttöä tai tiivistä kontekstia. |
| `/goal`, `/plan` | Hallitse tavoitteita ja vain luku -suunnittelutilaa. |
| `/diff`, `/undo`, `/redo` | Tarkastele muutoksia tai kumoa/toista agentin vuoro. |
| `/darwin`, `/council` | Käytä valinnaisia usean mallin työnkulkuja. |
| `/update check\|download\|install` | Tarkista, lataa tai asenna päivitys erikseen. |
| `/quit`, `/exit` | Poistu sovelluksesta. |

<!-- readme-unit:keys -->
`Enter` lähettää pyynnön; `Ctrl+C` keskeyttää. `PgUp`/`PgDn` ja hiiren rulla vierittävät; `Ctrl+F` hakee; `Shift+T` vaihtaa päättelyn näkyvyyttä; `Shift+E` laajentaa työkalutulostetta. Suorituksen aikana annetut pyynnöt jonotetaan seuraavaan turvalliseen vaiheeseen.

<!-- readme-unit:h.updates -->
## Päivitykset

<!-- readme-unit:update.check -->
Tarkista vakaat GitHub-julkaisut tarvittaessa GUI:n About-näkymästä, TUI:n `/update check`-komennolla tai CLI:n `--check-update`-valitsimella. Tarkistus ei asenna mitään.

<!-- readme-unit:update.download -->
Lataus valmistelee vastaavan siirrettävän paketin ja tarkistaa sen SHA256-arvon julkaisun manifestista. GUI:n lataus ja `/update download` jättävät käynnissä olevan asennuksen paikalleen.

<!-- readme-unit:update.install -->
Asenna valmisteltu päivitys GUI:n asennuspainikkeella tai komennolla `/update install`. CLI:n `--update` pyytää nimenomaisesti lataamista ja asentamista. Nykyinen `supercli-data/` säilytetään ja korvattavat ohjelmatiedostot varmuuskopioidaan paikallisesti.

<!-- readme-unit:update.restart -->
Käynnistä uudelleen käsin asennuksen jälkeen. Sulje muut kopiot ennen asennusta; käynnissä olevat ohjelmatiedostot voivat olla lukittuja Windowsissa. Jos vastaavaa pakettia tai tarkistusmetadataa ei ole saatavilla, käytä tarkistettua manuaalista julkaisulatausta. Säilytä oma varmuuskopio tiedoistasi.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentaatio

<!-- readme-unit:docs.start -->
Aloita [dokumentaatiohakemistosta](../README.md), [pikaoppaasta](../quickstart.md), [tietojen rakenteesta](../data-layout.md) ja [asetuksista](../configuration.md).

<!-- readme-unit:docs.engine -->
Lue toteutuksen yksityiskohdista [arkkitehtuuri](../architecture.md), [delegointi](../delegation.md), [suorituskyky](../performance.md), [GUI-suunnittelu](../webgui.md) ja [projektirakenne](../project-structure.md).

<!-- readme-unit:docs.extra -->
Ominaisuusoppaat: [siirrettävä MCP](../portable-mcp.md), [sisäänrakennetut taidot](../builtin-skills.md) ja [telemetria](../telemetry.md). Kehityksen seuranta: [suunnitelma](../PLAN.md) ja [tiekartta](../ROADMAP.md).

<!-- readme-unit:reference -->
[Aiempi täydellinen README](../readme-reference.md) säilytetään historiallisena viitteenä; vanhemmat ominaisuuskuvaukset voivat edeltää versiota `1.0.0`.

<!-- readme-unit:h.build -->
## Koonti ja tarkistus

<!-- readme-unit:build -->
Käytä tiedostossa `go.mod` määritettyä Go-versiota. Windowsissa `build.bat` rakentaa TUI:n, `build_ui.bat` GUI:n ja `run.bat` rakentaa tarvittaessa ja käynnistää päätteen.

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
Suorita Go-testit ennen julkaisua. Dokumentaatiotarkistin tarkistaa kaikki 27 käännöstä, yhdistetyn sisällön, otsikot, linkit, tekniset literaalit ja identtiset koodiesimerkit.

<!-- readme-unit:requirements -->
Normaali käyttö vaatii määritetyn mallipäätepisteen tai tilin. Git ja ripgrep parantavat tietovarastotyönkulkuja; ulkoiset työkalut ovat valinnaisia, ellei työnkulku tarvitse niitä. GUI vaatii lisäksi alustansa webview-/selaintuen. Tutki asennusta valitsimella `--doctor`.

<!-- readme-unit:h.license -->
## Lisenssi

<!-- readme-unit:license -->
SuperCli käyttää [MIT-lisenssiä](../../LICENSE). Mukana toimitetuilla riippuvuuksilla ja sisällöllä on omat ilmoituksensa; katso [kolmansien osapuolten ilmoitukset](../../THIRD_PARTY_NOTICES.md) ja [taito-opas](../builtin-skills.md).
