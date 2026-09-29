[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.0

<!-- readme-unit:intro -->
Go nyelven írt hordozható AI programozóügynök, közös motort használó terminálfelülettel (TUI), asztali/webes felülettel (GUI) és kötegelt móddal.

<!-- readme-unit:status -->
Ez a README az `1.0.0` verziót ismerteti. A hordozható kiadási csomagok a [GitHub Releases](https://github.com/snakex21/SuperCli/releases) oldalon terjeszthetők; a helyi fordítás nem jelenti, hogy a hozzá tartozó kiadás már megjelent.

<!-- readme-unit:h.start -->
## Első lépések

<!-- readme-unit:start -->
Csomagolja ki az operációs rendszerének és architektúrájának megfelelő csomagot egy írható mappába. Windows alatt a terminálhoz indítsa a `supercli.exe`, a GUI-hoz a `supercli-web.exe` fájlt. A mellékelt `supercli-data/` mappát tartsa a futtatható fájlok mellett.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Linux vagy macOS alatt használja a csomag megfelelő futtatható fájlját, vagy fordítsa le a forráskódot. A projektet a `--home` választja ki; egyetlen kéréshez TUI nélkül használja a `--batch` kapcsolót.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Hordozható adatok

<!-- readme-unit:data -->
A beállítások, munkamenetek, memória, hitelesítő adatok, gyorsítótárak, naplók és biztonsági mentések az alkalmazás melletti `supercli-data/` mappában vannak. Vigye át az egész alkalmazásmappát, hogy ezeket is magával vigye. Az alkalmazás állapotát nem a `%APPDATA%`, `%LOCALAPPDATA%` vagy a Windows beállításjegyzéke tárolja.

<!-- readme-unit:workspace -->
A `--home` és `SUPERCLI_HOME` a munkaterületet választja ki az alkalmazásadatok áthelyezése nélkül. A projekt felülíró beállításai és munkafájljai a `<project>/.supercli/` útvonalat használják.

<!-- readme-unit:override -->
Az adatok helyét csak a kifejezetten megadott `--data-dir` vagy `SUPERCLI_DATA_DIR` módosítja. Ha az alkalmazásmappa nem írható, az indítás hibát jelez, és nem vált észrevétlenül profilmappára.

<!-- readme-unit:legacy -->
A terminál első indításkor átmásolhatja a régi `~/.supercli` adatokat egy üres hordozható könyvtárba, az eredeti megtartásával. Lásd az [adatok elrendezését](../data-layout.md).

<!-- readme-unit:secrets -->
A hitelesítő adatok együtt mozognak a hordozható mappával. Tartsa azt privátként, készítsen mentéseket, és soha ne tegye az API-kulcsokat vagy hitelesítési fájlokat nyilvános adattárba.

<!-- readme-unit:h.config -->
## Modellek és konfiguráció

<!-- readme-unit:providers -->
A szolgáltatókat a GUI beállításaiban vagy a TUI `/providers` és `/models` parancsaival állítsa be. Támogatottak az OpenAI-kompatibilis végpontok, a natív Anthropic, a ChatGPT/Codex OAuth, az opencode átjárók és egy offline echo szolgáltató.

<!-- readme-unit:config -->
A globális beállítások a `supercli-data/config.toml` fájlban vannak; a `<project>/.supercli/config.toml` felülírhatja őket. A környezeti változók és CLI-kapcsolók elsőbbséget élveznek. Példa helyi végpontra:

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
Cserélje le a példabeli végpontot és modellt a szerver értékeire. A felhőszolgáltatások hitelesítő adatokat kérhetnek és használati díjat számíthatnak fel. A modell képességei határozzák meg a képfelismerést, eszközöket, következtetést és kontextuskorlátokat. Lásd a [konfigurációt](../configuration.md).

<!-- readme-unit:h.features -->
## Funkciók

<!-- readme-unit:surface -->
A GUI és TUI ugyanazt a 27 felületi nyelvet támogatja. Folyamatosan megjelenő beszélgetéseket, munkamenet-visszaállítást, projektválasztást, modellkezelést, mellékleteket és használati nézeteket kínálnak. A TUI támogatja az egérgörgetést, a beszélgetéskeresést és az összecsukható gondolkodási/eszközkimenetet.

<!-- readme-unit:agent -->
A motor támogatja az eszközfelderítést, ellenőrzött fájlszerkesztést, parancsvégrehajtást, munkamenet-előzményeket, projektmemóriát, kontextustömörítést, célokat, munkavégző ügynököknek delegálást, konzultációt és opcionális vázlatmodelleket.

<!-- readme-unit:tools -->
Az eszközök kódkeresést, célzott fájlolvasást és javítást, képeket, ZIP-archívumokat, DOCX/XLSX/PDF dokumentumokat és korlátozott kontextus-végrehajtást fednek le. Az elérhető eszközök a kiválasztott profiltól és modelltől függnek; az aktuális katalógust eszközfelderítéssel érheti el.

<!-- readme-unit:extensions -->
Az opcionális MCP-csomagok a `supercli-data/mcp/` alatt vannak, és használatkor indulnak. A beépített képességek archívuma a `supercli-data/skills/builtin-skills.zip`; hiánya nem akadályozza a normál indítást. A bővítmények saját futtatókörnyezetet vagy telepített gazdaalkalmazásokat igényelhetnek.

<!-- readme-unit:optional -->
A Darwin jelöltgenerálás, a tanácsok és a munkavégző ügynököknek delegálás opcionális folyamatok. A párhuzamos modellhívások növelhetik az erőforrásigényt és a költséget. Az alap futtatható fájl nem igényel Node, Python, Docker vagy CGO környezetet.

<!-- readme-unit:h.controls -->
## Parancsok és vezérlés

<!-- readme-unit:controls -->
A parancspalettához írjon `/` jelet. A TUI műveletközpontját üres bevitelnél `Tab` vagy `Ctrl+K` nyitja meg; a nyilakkal válasszon műveletet, az `Esc` visszalép.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Parancs | Művelet |
| --- | --- |
| `/help`, `/doctor` | Súgó és diagnosztika megjelenítése. |
| `/model`, `/models`, `/providers` | Modellek kiválasztása és szolgáltatók kezelése. |
| `/resume`, `/export` | Beszélgetés folytatása vagy exportálása. |
| `/status`, `/cost`, `/compact` | Használat megtekintése vagy kontextus tömörítése. |
| `/goal`, `/plan` | Célok és csak olvasható tervezési mód kezelése. |
| `/diff`, `/undo`, `/redo` | Változások megtekintése vagy ügynöklépés visszavonása/ismétlése. |
| `/darwin`, `/council` | Opcionális többmodelles folyamatok használata. |
| `/update check\|download\|install` | Frissítés keresése, letöltése vagy kifejezett telepítése. |
| `/quit`, `/exit` | Kilépés az alkalmazásból. |

<!-- readme-unit:keys -->
Az `Enter` elküldi a kérést; a `Ctrl+C` megszakít. A `PgUp`/`PgDn` és az egérgörgő görget; a `Ctrl+F` keres; a `Shift+T` váltja a gondolkodást; a `Shift+E` kibontja az eszközkimenetet. A futás közben megadott kérések a következő biztonságos lépésre sorba állnak.

<!-- readme-unit:h.updates -->
## Frissítések

<!-- readme-unit:update.check -->
Igény szerint keressen stabil GitHub-kiadásokat a GUI About képernyőjén, a TUI `/update check` vagy a CLI `--check-update` használatával. Az ellenőrzés semmit nem telepít.

<!-- readme-unit:update.download -->
A letöltés előkészíti a megfelelő hordozható csomagot, és ellenőrzi a SHA256 értékét a kiadási jegyzék alapján. A GUI letöltése és a `/update download` érintetlenül hagyja a futó telepítést.

<!-- readme-unit:update.install -->
Az előkészített frissítést a GUI telepítésgombjával vagy a `/update install` paranccsal telepítse. A CLI `--update` kifejezetten letöltést és telepítést kér. A meglévő `supercli-data/` megmarad, a lecserélt futtatható fájlokról helyi mentés készül.

<!-- readme-unit:update.restart -->
Telepítés után indítsa újra kézzel. Telepítés előtt zárja be a többi példányt; Windows alatt a futó végrehajtható fájlok zároltak lehetnek. Ha nincs megfelelő csomag vagy ellenőrzési metaadat, használjon ellenőrzött kézi kiadásletöltést. Tartson saját adatmentést.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentáció

<!-- readme-unit:docs.start -->
Kezdje a [dokumentációs tartalomjegyzékkel](../README.md), a [gyorsindítással](../quickstart.md), az [adatok elrendezésével](../data-layout.md) és a [konfigurációval](../configuration.md).

<!-- readme-unit:docs.engine -->
Megvalósítási részletek: [architektúra](../architecture.md), [delegálás](../delegation.md), [teljesítmény](../performance.md), [GUI-tervezés](../webgui.md) és [projektstruktúra](../project-structure.md).

<!-- readme-unit:docs.extra -->
Funkcióútmutatók: [hordozható MCP](../portable-mcp.md), [beépített képességek](../builtin-skills.md) és [telemetria](../telemetry.md). Fejlesztéskövetés: [terv](../PLAN.md) és [ütemterv](../ROADMAP.md).

<!-- readme-unit:reference -->
Az [előző teljes README](../readme-reference.md) történeti hivatkozásként megmarad; a régebbi funkcióleírások az `1.0.0` verzió előttről származhatnak.

<!-- readme-unit:h.build -->
## Fordítás és ellenőrzés

<!-- readme-unit:build -->
Használja a `go.mod` által megadott Go-verziót. Windows alatt a `build.bat` a TUI-t, a `build_ui.bat` a GUI-t fordítja; a `run.bat` szükség esetén fordít és elindítja a terminált.

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
Kiadás előtt futtassa a Go-teszteket. A dokumentációellenőrző mind a 27 fordítást, a megfeleltetett tartalmat, címsorokat, linkeket, technikai literálokat és azonos kódpéldákat ellenőrzi.

<!-- readme-unit:requirements -->
A normál használathoz beállított modellvégpont vagy fiók szükséges. A Git és ripgrep javítja az adattárakkal végzett munkát; külső eszközök csak az őket igénylő folyamatokhoz szükségesek. A GUI emellett a platform webview/böngésző támogatását igényli. A telepítés diagnosztikájához használja a `--doctor` kapcsolót.

<!-- readme-unit:h.license -->
## Licenc

<!-- readme-unit:license -->
A SuperCli az [MIT-licencet](../../LICENSE) használja. A mellékelt függőségeknek és tartalomnak saját közleményei vannak; lásd a [harmadik felek közleményeit](../../THIRD_PARTY_NOTICES.md) és a [képességútmutatót](../builtin-skills.md).
