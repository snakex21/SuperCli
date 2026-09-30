[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.1

<!-- readme-unit:intro -->
Портативний AI-агент для програмування, написаний на Go, з термінальним інтерфейсом (TUI), настільним/вебінтерфейсом (GUI) та пакетним режимом, що використовують спільний рушій.

<!-- readme-unit:status -->
Цей README описує версію `1.0.1`. Портативні пакети випусків поширюються через [GitHub Releases](https://github.com/snakex21/SuperCli/releases); локальна збірка не означає, що відповідний випуск уже опублікований.

<!-- readme-unit:h.screenshots -->
## Знімки екрана

<!-- readme-unit:screenshot.gui -->
![Вебінтерфейс SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Центр дій у терміналі SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Зображення GUI — це знімок екрана; зображення TUI — візуалізація фактичного компонування термінального інтерфейсу застосунку. [Більше знімків екрана](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Початок роботи

<!-- readme-unit:start -->
Розпакуйте пакет для своєї операційної системи й архітектури в папку з правом запису. У Windows запустіть `supercli.exe` для термінала або `supercli-web.exe` для GUI. Залиште включену папку `supercli-data/` поруч із виконуваними файлами.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
У Linux або macOS використовуйте відповідний виконуваний файл із пакета або зберіть із вихідного коду. Виберіть проєкт через `--home`; використовуйте `--batch` для одного запиту без TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Портативні дані

<!-- readme-unit:data -->
Налаштування, сеанси, пам'ять, облікові дані, кеші, журнали й резервні копії зберігаються в `supercli-data/` поруч із застосунком. Перенесіть усю папку застосунку, щоб забрати їх із собою. Застосунок не використовує `%APPDATA%`, `%LOCALAPPDATA%` або реєстр Windows для свого стану.

<!-- readme-unit:workspace -->
`--home` і `SUPERCLI_HOME` вибирають робочу область без переміщення даних застосунку. Перевизначення налаштувань проєкту й робочі артефакти використовують `<project>/.supercli/`.

<!-- readme-unit:override -->
Розташування даних змінює лише явно заданий `--data-dir` або `SUPERCLI_DATA_DIR`. Якщо папка застосунку недоступна для запису, запуск повідомляє про помилку замість непомітного переходу до каталогу профілю.

<!-- readme-unit:legacy -->
Під час першого запуску термінал може скопіювати старі дані `~/.supercli` до порожнього портативного каталогу, зберігаючи оригінал. Див. [структуру даних](../data-layout.md).

<!-- readme-unit:secrets -->
Облікові дані переносяться разом із портативною папкою. Зберігайте її приватність, робіть резервні копії й ніколи не додавайте API-ключі чи файли автентифікації до публічного репозиторію.

<!-- readme-unit:h.config -->
## Моделі й налаштування

<!-- readme-unit:providers -->
Налаштуйте постачальників через GUI або TUI `/providers` і `/models`. Підтримуються OpenAI-сумісні кінцеві точки, нативний Anthropic, ChatGPT/Codex OAuth, шлюзи opencode та автономний постачальник echo.

<!-- readme-unit:config -->
Глобальні налаштування містяться в `supercli-data/config.toml`; `<project>/.supercli/config.toml` може їх перевизначити. Змінні середовища та прапорці CLI мають пріоритет. Приклад локальної кінцевої точки:

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
Замініть кінцеву точку й модель із прикладу значеннями свого сервера. Хмарні сервіси можуть вимагати облікові дані й стягувати плату за використання. Можливості моделі визначають зір, інструменти, міркування та межі контексту. Див. [налаштування](../configuration.md).

<!-- readme-unit:h.features -->
## Можливості

<!-- readme-unit:surface -->
GUI й TUI підтримують ті самі 27 мов інтерфейсу. Вони надають потокові розмови, відновлення сеансів, вибір проєктів, керування моделями, вкладення та перегляд використання. TUI підтримує прокручування мишею, пошук у розмові й згортання виводу міркувань/інструментів.

<!-- readme-unit:agent -->
Рушій підтримує пошук інструментів, перевірені зміни файлів, виконання команд, історію сеансів, пам'ять проєкту, ущільнення контексту, цілі, делегування робочим агентам, консультації та необов'язкові чернеткові моделі.

<!-- readme-unit:tools -->
Інструменти охоплюють пошук коду, цільове читання й виправлення файлів, зображення, ZIP-архіви, документи DOCX/XLSX/PDF та обмежене виконання в контексті. Доступні інструменти залежать від обраного профілю й моделі; використовуйте пошук інструментів для актуального каталогу.

<!-- readme-unit:extensions -->
Необов'язкові пакети MCP містяться в `supercli-data/mcp/` і запускаються під час використання. Архів вбудованих навичок розташований у `supercli-data/skills/builtin-skills.zip`; його відсутність не перешкоджає звичайному запуску. Розширення можуть потребувати власних середовищ виконання або встановлених програм-хостів.

<!-- readme-unit:optional -->
Генерування кандидатів Darwin, ради моделей і делегування робочим агентам — необов'язкові процеси. Паралельні виклики моделей можуть збільшити використання ресурсів і вартість. Основний виконуваний файл не потребує Node, Python, Docker або CGO.

<!-- readme-unit:h.controls -->
## Команди й керування

<!-- readme-unit:controls -->
Введіть `/` для палітри команд. Відкрийте центр дій TUI клавішею `Tab` за порожнього вводу або `Ctrl+K`; вибирайте дії стрілками й повертайтеся через `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Команда | Дія |
| --- | --- |
| `/help`, `/doctor` | Показати довідку й діагностику. |
| `/model`, `/models`, `/providers` | Вибрати моделі й керувати постачальниками. |
| `/resume`, `/export` | Продовжити або експортувати розмову. |
| `/status`, `/cost`, `/compact` | Переглянути використання або ущільнити контекст. |
| `/goal`, `/plan` | Керувати цілями й режимом планування лише для читання. |
| `/diff`, `/undo`, `/redo` | Переглянути зміни або скасувати/повторити хід агента. |
| `/darwin`, `/council` | Використати необов'язкові процеси з кількома моделями. |
| `/update check\|download\|install` | Перевірити, завантажити або явно встановити оновлення. |
| `/quit`, `/exit` | Вийти із застосунку. |

<!-- readme-unit:keys -->
`Enter` надсилає запит; `Ctrl+C` перериває. `PgUp`/`PgDn` і коліщатко миші прокручують; `Ctrl+F` шукає; `Shift+T` перемикає міркування; `Shift+E` розгортає вивід інструментів. Запити під час виконання потрапляють у чергу для наступного безпечного кроку.

<!-- readme-unit:h.updates -->
## Оновлення

<!-- readme-unit:update.check -->
Перевіряйте стабільні випуски GitHub на запит на екрані About в GUI, через TUI `/update check` або CLI `--check-update`. Перевірка нічого не встановлює.

<!-- readme-unit:update.download -->
Завантаження готує відповідний портативний пакет і перевіряє його SHA256 за маніфестом випуску. Завантаження в GUI та `/update download` залишають поточну інсталяцію на місці.

<!-- readme-unit:update.install -->
Щоб установити підготовлене оновлення, використайте кнопку GUI або `/update install`. CLI `--update` явно запитує завантаження й встановлення. Наявний `supercli-data/` зберігається, а замінені виконувані файли отримують локальні резервні копії.

<!-- readme-unit:update.restart -->
Після встановлення перезапустіть вручну. Перед встановленням закрийте інші копії; запущені виконувані файли можуть бути заблоковані у Windows. Якщо відповідний пакет або метадані перевірки недоступні, використайте перевірене ручне завантаження випуску. Зберігайте власну резервну копію даних.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Документація

<!-- readme-unit:docs.start -->
Почніть з [індексу документації](../README.md), [швидкого старту](../quickstart.md), [структури даних](../data-layout.md) та [налаштувань](../configuration.md).

<!-- readme-unit:docs.engine -->
Подробиці реалізації: [архітектура](../architecture.md), [делегування](../delegation.md), [продуктивність](../performance.md), [дизайн GUI](../webgui.md) та [структура проєкту](../project-structure.md).

<!-- readme-unit:docs.extra -->
Посібники з функцій: [портативний MCP](../portable-mcp.md), [вбудовані навички](../builtin-skills.md) та [телеметрія](../telemetry.md). Відстеження розробки: [план](../PLAN.md) і [дорожня карта](../ROADMAP.md).

<!-- readme-unit:reference -->
[Попередній повний README](../readme-reference.md) збережено як історичне джерело; старі описи функцій можуть передувати версії `1.0.0`.

<!-- readme-unit:h.build -->
## Збирання й перевірка

<!-- readme-unit:build -->
Використовуйте версію Go, зазначену в `go.mod`. У Windows `build.bat` збирає TUI, `build_ui.bat` — GUI, а `run.bat` за потреби збирає й запускає термінал.

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
Перед випуском запустіть тести Go. Перевірка документації перевіряє всі 27 перекладів, відповідність вмісту, заголовки, посилання, технічні літерали та однакові приклади коду.

<!-- readme-unit:requirements -->
Звичайне використання потребує налаштованої кінцевої точки моделі або облікового запису. Git і ripgrep поліпшують роботу з репозиторіями; зовнішні інструменти необов'язкові, якщо процес їх не потребує. GUI додатково вимагає підтримку webview/браузера платформи. Використовуйте `--doctor` для діагностики інсталяції.

<!-- readme-unit:h.license -->
## Ліцензія

<!-- readme-unit:license -->
SuperCli використовує [ліцензію MIT](../../LICENSE). Включені залежності й вміст мають власні повідомлення; див. [повідомлення третіх сторін](../../THIRD_PARTY_NOTICES.md) та [посібник із навичок](../builtin-skills.md).
