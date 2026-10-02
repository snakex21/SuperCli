[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.2

<!-- readme-unit:intro -->
Go ile yazılmış; aynı motoru paylaşan terminal arayüzü (TUI), masaüstü/web arayüzü (GUI) ve toplu işlem modu sunan taşınabilir bir yapay zekâ kodlama ajanı.

<!-- readme-unit:status -->
Bu README `1.0.2` sürümünü açıklar. Taşınabilir sürüm paketleri [GitHub Releases](https://github.com/snakex21/SuperCli/releases) üzerinden dağıtılır; yerel derleme, ilgili sürümün zaten yayımlandığı anlamına gelmez.

<!-- readme-unit:h.screenshots -->
## Ekran görüntüleri

<!-- readme-unit:screenshot.gui -->
![SuperCli web arayüzü (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![SuperCli terminal eylem merkezi (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
GUI görseli bir ekran görüntüsüdür; TUI görseli uygulamanın gerçek terminal düzeninin işlenmiş görüntüsüdür. [Diğer ekran görüntüleri](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Başlangıç

<!-- readme-unit:start -->
İşletim sisteminize ve mimarinize uygun paketi yazılabilir bir klasöre çıkarın. Windows'ta terminal için `supercli.exe`, GUI için `supercli-web.exe` başlatın. Birlikte gelen `supercli-data/` klasörünü yürütülebilir dosyaların yanında tutun.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Linux veya macOS'ta paketteki uygun yürütülebilir dosyayı kullanın ya da kaynak koddan derleyin. Projenizi `--home` ile seçin; TUI olmadan tek bir istem için `--batch` kullanın.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Taşınabilir veriler

<!-- readme-unit:data -->
Ayarlar, oturumlar, bellek, kimlik bilgileri, önbellekler, günlükler ve yedekler uygulamanın yanındaki `supercli-data/` klasöründedir. Bunları yanınızda taşımak için uygulama klasörünün tamamını taşıyın. Uygulama durumunu saklamak için `%APPDATA%`, `%LOCALAPPDATA%` veya Windows kayıt defterini kullanmaz.

<!-- readme-unit:workspace -->
`--home` ve `SUPERCLI_HOME`, uygulama verilerini taşımadan çalışma alanını seçer. Projeye özgü ayar geçersiz kılmaları ve çalışma dosyaları `<project>/.supercli/` kullanır.

<!-- readme-unit:override -->
Veri konumunu yalnızca açıkça belirtilen `--data-dir` veya `SUPERCLI_DATA_DIR` değiştirir. Uygulama klasörü yazılabilir değilse başlangıç, sessizce profil dizini kullanmak yerine hata bildirir.

<!-- readme-unit:legacy -->
Terminal ilk başlangıçta eski `~/.supercli` verilerini boş bir taşınabilir dizine kopyalayabilir ve asıllarını korur. [Veri düzenine](../data-layout.md) bakın.

<!-- readme-unit:secrets -->
Kimlik bilgileri taşınabilir klasörle birlikte taşınır. Klasörü özel tutun, yedekleyin ve API anahtarlarını veya kimlik doğrulama dosyalarını asla herkese açık bir depoya kaydetmeyin.

<!-- readme-unit:h.config -->
## Modeller ve yapılandırma

<!-- readme-unit:providers -->
Sağlayıcıları GUI ayarlarından veya TUI `/providers` ve `/models` ile yapılandırın. OpenAI uyumlu uç noktalar, yerel Anthropic bağlantısı, ChatGPT/Codex OAuth, opencode ağ geçitleri ve çevrimdışı echo sağlayıcısı desteklenir.

<!-- readme-unit:config -->
Genel ayarlar `supercli-data/config.toml` içindedir; `<project>/.supercli/config.toml` bunları geçersiz kılabilir. Ortam değişkenleri ve CLI seçenekleri önceliklidir. Yerel uç nokta örneği:

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
Örnek uç noktayı ve modeli sunucunuzun değerleriyle değiştirin. Bulut hizmetleri kimlik bilgileri isteyebilir ve kullanım ücreti alabilir. Model yetenekleri görüntü algılama, araçlar, akıl yürütme ve bağlam sınırlarını belirler. [Yapılandırmaya](../configuration.md) bakın.

<!-- readme-unit:h.features -->
## Özellikler

<!-- readme-unit:surface -->
GUI ve TUI aynı 27 arayüz dilini destekler. Akışlı sohbetler, oturum kurtarma, proje seçimi, model yönetimi, ekler ve kullanım görünümleri sunar. TUI fareyle kaydırma, sohbet araması ve daraltılabilir düşünme/araç çıktısını destekler.

<!-- readme-unit:agent -->
Motor; araç keşfi, doğrulanmış dosya düzenlemeleri, komut çalıştırma, oturum geçmişi, proje belleği, bağlam sıkıştırma, hedefler, işçi ajanlara görev devri, danışma ve isteğe bağlı taslak modellerini destekler.

<!-- readme-unit:tools -->
Araçlar; kod araması, hedefli dosya okumaları ve yamalar, görüntüler, ZIP arşivleri, DOCX/XLSX/PDF belgeleri ve sınırlı bağlam yürütmeyi kapsar. Kullanılabilir araçlar seçilen profil ve modele bağlıdır; güncel katalog için araç keşfini kullanın.

<!-- readme-unit:extensions -->
İsteğe bağlı MCP paketleri `supercli-data/mcp/` içinde bulunur ve kullanıldığında başlar. Yerleşik beceri arşivi `supercli-data/skills/builtin-skills.zip` içindedir; yokluğu normal başlangıcı engellemez. Uzantılar kendi çalışma ortamlarını veya kurulu ana uygulamaları gerektirebilir.

<!-- readme-unit:optional -->
Darwin aday üretimi, model konseyleri ve işçi ajanlara görev devri isteğe bağlı iş akışlarıdır. Paralel model çağrıları kaynak kullanımını ve maliyeti artırabilir. Temel yürütülebilir dosya Node, Python, Docker veya CGO gerektirmez.

<!-- readme-unit:h.controls -->
## Komutlar ve kontroller

<!-- readme-unit:controls -->
Komut paleti için `/` yazın. TUI eylem merkezini boş girişte `Tab` veya `Ctrl+K` ile açın; oklarla eylem seçin ve `Esc` ile geri dönün.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Komut | Eylem |
| --- | --- |
| `/help`, `/doctor` | Yardım ve tanılamayı göster. |
| `/model`, `/models`, `/providers` | Model seç ve sağlayıcıları yönet. |
| `/resume`, `/export` | Sohbeti sürdür veya dışa aktar. |
| `/status`, `/cost`, `/compact` | Kullanımı incele veya bağlamı sıkıştır. |
| `/goal`, `/plan` | Hedefleri ve salt okunur plan modunu yönet. |
| `/diff`, `/undo`, `/redo` | Değişiklikleri incele veya ajan turunu geri al/yinele. |
| `/darwin`, `/council` | İsteğe bağlı çok modelli iş akışlarını kullan. |
| `/update check\|download\|install` | Güncellemeyi denetle, indir veya açıkça yükle. |
| `/quit`, `/exit` | Uygulamadan çık. |

<!-- readme-unit:keys -->
`Enter` istem gönderir; `Ctrl+C` keser. `PgUp`/`PgDn` ve fare tekerleği kaydırır; `Ctrl+F` arar; `Shift+T` düşünmeyi açıp kapatır; `Shift+E` araç çıktısını genişletir. Çalışma sırasında girilen istemler sonraki güvenli adım için sıraya alınır.

<!-- readme-unit:h.updates -->
## Güncellemeler

<!-- readme-unit:update.check -->
Kararlı GitHub sürümlerini isteğe bağlı olarak GUI About ekranından, TUI `/update check` veya CLI `--check-update` ile denetleyin. Denetleme hiçbir şey yüklemez.

<!-- readme-unit:update.download -->
İndirme, eşleşen taşınabilir paketi hazırlar ve SHA256 değerini sürüm manifestine göre doğrular. GUI indirmesi ve `/update download`, çalışan kurulumu yerinde bırakır.

<!-- readme-unit:update.install -->
Hazırlanan güncellemeyi GUI yükleme düğmesi veya `/update install` ile yükleyin. CLI `--update` açıkça indirme ve yükleme ister. Mevcut `supercli-data/` korunur ve değiştirilen yürütülebilir dosyalar yerel olarak yedeklenir.

<!-- readme-unit:update.restart -->
Yüklemeden sonra elle yeniden başlatın. Yüklemeden önce diğer kopyaları kapatın; çalışan yürütülebilir dosyalar Windows'ta kilitli olabilir. Uygun paket veya doğrulama meta verileri yoksa doğrulanmış bir elle sürüm indirmesi kullanın. Kendi veri yedeğinizi saklayın.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Belgeler

<!-- readme-unit:docs.start -->
[Belge dizini](../README.md), [hızlı başlangıç](../quickstart.md), [veri düzeni](../data-layout.md) ve [yapılandırma](../configuration.md) ile başlayın.

<!-- readme-unit:docs.engine -->
Uygulama ayrıntıları için [mimari](../architecture.md), [görev devri](../delegation.md), [performans](../performance.md), [GUI tasarımı](../webgui.md) ve [proje yapısını](../project-structure.md) okuyun.

<!-- readme-unit:docs.extra -->
Özellik kılavuzları: [taşınabilir MCP](../portable-mcp.md), [yerleşik beceriler](../builtin-skills.md) ve [telemetri](../telemetry.md). Geliştirme takibi: [plan](../PLAN.md) ve [yol haritası](../ROADMAP.md).

<!-- readme-unit:reference -->
[Önceki tam README](../readme-reference.md) tarihsel başvuru olarak korunur; eski özellik açıklamaları `1.0.0` sürümünden öncesine ait olabilir.

<!-- readme-unit:h.build -->
## Derleme ve doğrulama

<!-- readme-unit:build -->
`go.mod` içinde belirtilen Go sürümünü kullanın. Windows'ta `build.bat` TUI'yi, `build_ui.bat` GUI'yi derler; `run.bat` gerekirse derler ve terminali başlatır.

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
Sürümden önce Go testlerini çalıştırın. Belge denetleyicisi tüm 27 çeviriyi, eşlenmiş içeriği, başlıkları, bağlantıları, teknik sabitleri ve özdeş kod örneklerini doğrular.

<!-- readme-unit:requirements -->
Normal kullanım yapılandırılmış bir model uç noktası veya hesap gerektirir. Git ve ripgrep depo iş akışlarını iyileştirir; dış araçlar, iş akışı gerektirmedikçe isteğe bağlıdır. GUI ayrıca platformun webview/tarayıcı desteğini gerektirir. Kurulum tanılaması için `--doctor` kullanın.

<!-- readme-unit:h.license -->
## Lisans

<!-- readme-unit:license -->
SuperCli [MIT Lisansı](../../LICENSE) kullanır. Birlikte gelen bağımlılıkların ve içeriğin kendi bildirimleri vardır; [üçüncü taraf bildirimlerine](../../THIRD_PARTY_NOTICES.md) ve [beceri kılavuzuna](../builtin-skills.md) bakın.
