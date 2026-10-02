[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.3

<!-- readme-unit:intro -->
Портативный AI-агент для программирования на Go: терминальный интерфейс (TUI), настольный/веб-интерфейс (GUI) и пакетный режим используют единый движок.

<!-- readme-unit:status -->
Этот README описывает версию `1.0.3`. Портативные пакеты распространяются через [GitHub Releases](https://github.com/snakex21/SuperCli/releases); локальная сборка не означает, что соответствующий выпуск уже опубликован.

<!-- readme-unit:h.screenshots -->
## Снимки экрана

<!-- readme-unit:screenshot.gui -->
![Веб-интерфейс SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Центр действий в терминале SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Изображение GUI — это снимок экрана; изображение TUI — визуализация фактической компоновки терминального интерфейса приложения. [Больше снимков экрана](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Начало работы

<!-- readme-unit:start -->
Распакуйте пакет для своей ОС и архитектуры в папку с правом записи. В Windows запустите `supercli.exe` для терминала или `supercli-web.exe` для GUI. Оставьте включённую папку `supercli-data/` рядом с исполняемыми файлами.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
В Linux или macOS используйте соответствующий исполняемый файл из пакета или соберите исходный код. Выберите проект через `--home`; используйте `--batch` для одного запроса без TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Портативные данные

<!-- readme-unit:data -->
Настройки, сеансы, память, учётные данные, кэши, журналы и резервные копии находятся в `supercli-data/` рядом с приложением. Переместите всю папку приложения, чтобы перенести их. Приложение не использует `%APPDATA%`, `%LOCALAPPDATA%` или реестр Windows для своего состояния.

<!-- readme-unit:workspace -->
`--home` и `SUPERCLI_HOME` выбирают рабочую область без переноса данных приложения. Переопределения настроек проекта и рабочие артефакты используют `<project>/.supercli/`.

<!-- readme-unit:override -->
Только явно заданные `--data-dir` или `SUPERCLI_DATA_DIR` меняют расположение данных. Если папка приложения недоступна для записи, запуск сообщает об ошибке вместо незаметного перехода в каталог профиля.

<!-- readme-unit:legacy -->
При первом запуске терминал может скопировать старые данные `~/.supercli` в пустой портативный каталог, сохранив оригинал. См. [структуру данных](../data-layout.md).

<!-- readme-unit:secrets -->
Учётные данные переносятся вместе с портативной папкой. Сохраняйте её конфиденциальность, делайте резервные копии и никогда не добавляйте API-ключи или файлы аутентификации в публичный репозиторий.

<!-- readme-unit:h.config -->
## Модели и настройки

<!-- readme-unit:providers -->
Настройте провайдеров в GUI или через TUI `/providers` и `/models`. Поддерживаются OpenAI-совместимые конечные точки, нативный Anthropic, ChatGPT/Codex OAuth, шлюзы opencode и автономный echo-провайдер.

<!-- readme-unit:config -->
Глобальные настройки находятся в `supercli-data/config.toml`; `<project>/.supercli/config.toml` может их переопределить. Переменные среды и флаги CLI имеют приоритет. Пример локальной конечной точки:

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
Замените конечную точку и модель из примера значениями своего сервера. Облачные службы могут требовать учётные данные и взимать плату за использование. Возможности модели определяют зрение, инструменты, рассуждения и пределы контекста. См. [настройки](../configuration.md).

<!-- readme-unit:h.features -->
## Возможности

<!-- readme-unit:surface -->
GUI и TUI поддерживают одинаковые 27 языков интерфейса. Они предоставляют потоковые диалоги, восстановление сеансов, выбор проектов, управление моделями, вложения и представления расхода. TUI поддерживает прокрутку мышью, поиск в диалоге и сворачиваемый вывод рассуждений/инструментов.

<!-- readme-unit:agent -->
Движок поддерживает поиск инструментов, проверенные изменения файлов, выполнение команд, историю сеансов, память проекта, сжатие контекста, цели, делегирование рабочим агентам, консультации и необязательные черновые модели.

<!-- readme-unit:tools -->
Инструменты охватывают поиск кода, адресное чтение и исправление файлов, изображения, ZIP-архивы, документы DOCX/XLSX/PDF и ограниченное выполнение в контексте. Доступность зависит от выбранного профиля и модели; используйте поиск инструментов для актуального каталога.

<!-- readme-unit:extensions -->
Необязательные MCP-пакеты находятся в `supercli-data/mcp/` и запускаются при использовании. Архив встроенных навыков расположен в `supercli-data/skills/builtin-skills.zip`; его отсутствие не мешает обычному запуску. Расширения могут требовать собственные среды выполнения или установленные приложения-хосты.

<!-- readme-unit:optional -->
Генерация кандидатов Darwin, советы моделей и делегирование рабочим агентам — необязательные процессы. Параллельные вызовы моделей могут увеличивать расход ресурсов и стоимость. Основной исполняемый файл не требует Node, Python, Docker или CGO.

<!-- readme-unit:h.controls -->
## Команды и управление

<!-- readme-unit:controls -->
Введите `/` для палитры команд. Откройте центр действий TUI клавишей `Tab` при пустом вводе или `Ctrl+K`; выбирайте действия стрелками, возвращайтесь через `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Команда | Действие |
| --- | --- |
| `/help`, `/doctor` | Показать справку и диагностику. |
| `/model`, `/models`, `/providers` | Выбрать модели и управлять провайдерами. |
| `/resume`, `/export` | Продолжить или экспортировать диалог. |
| `/status`, `/cost`, `/compact` | Проверить расход или сжать контекст. |
| `/goal`, `/plan` | Управлять целями и режимом планирования только для чтения. |
| `/diff`, `/undo`, `/redo` | Просмотреть изменения или отменить/повторить ход агента. |
| `/darwin`, `/council` | Использовать необязательные процессы с несколькими моделями. |
| `/update check\|download\|install` | Проверить, скачать или явно установить обновление. |
| `/quit`, `/exit` | Выйти из приложения. |

<!-- readme-unit:keys -->
`Enter` отправляет запрос; `Ctrl+C` прерывает. `PgUp`/`PgDn` и колесо мыши прокручивают; `Ctrl+F` ищет; `Shift+T` переключает рассуждения; `Shift+E` разворачивает вывод инструментов. Запросы во время выполнения ставятся в очередь для следующего безопасного шага.

<!-- readme-unit:h.updates -->
## Обновления

<!-- readme-unit:update.check -->
Проверяйте стабильные выпуски GitHub по запросу на экране About в GUI, через TUI `/update check` или CLI `--check-update`. Проверка ничего не устанавливает.

<!-- readme-unit:update.download -->
Скачивание подготавливает подходящий портативный пакет и проверяет его SHA256 по манифесту выпуска. Скачивание в GUI и `/update download` сохраняют работающую установку на месте.

<!-- readme-unit:update.install -->
Для установки подготовленного обновления используйте кнопку GUI или `/update install`. CLI `--update` явно запрашивает скачивание и установку. Существующий `supercli-data/` сохраняется, а заменяемые исполняемые файлы получают локальные резервные копии.

<!-- readme-unit:update.restart -->
После установки перезапустите вручную. Перед установкой закройте другие копии; работающие исполняемые файлы могут быть заблокированы в Windows. Если подходящий пакет или метаданные проверки отсутствуют, используйте проверенное ручное скачивание выпуска. Храните собственную резервную копию данных.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Документация

<!-- readme-unit:docs.start -->
Начните с [оглавления документации](../README.md), [быстрого старта](../quickstart.md), [структуры данных](../data-layout.md) и [настроек](../configuration.md).

<!-- readme-unit:docs.engine -->
Подробности реализации: [архитектура](../architecture.md), [делегирование](../delegation.md), [производительность](../performance.md), [дизайн GUI](../webgui.md) и [структура проекта](../project-structure.md).

<!-- readme-unit:docs.extra -->
Руководства по функциям: [портативный MCP](../portable-mcp.md), [встроенные навыки](../builtin-skills.md) и [телеметрия](../telemetry.md). Отслеживание разработки: [план](../PLAN.md) и [дорожная карта](../ROADMAP.md).

<!-- readme-unit:reference -->
[Предыдущий полный README](../readme-reference.md) сохранён как историческая справка; старые описания функций могут предшествовать версии `1.0.0`.

<!-- readme-unit:h.build -->
## Сборка и проверка

<!-- readme-unit:build -->
Используйте версию Go, указанную в `go.mod`. В Windows `build.bat` собирает TUI, `build_ui.bat` — GUI, а `run.bat` собирает при необходимости и запускает терминал.

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
Перед выпуском запустите тесты Go. Проверка документации проверяет все 27 переводов, соответствие содержания, заголовки, ссылки, технические литералы и одинаковые примеры кода.

<!-- readme-unit:requirements -->
Обычное использование требует настроенную конечную точку модели или аккаунт. Git и ripgrep улучшают работу с репозиториями; внешние инструменты необязательны, если процесс их не требует. GUI дополнительно требует поддержку webview/браузера на платформе. Используйте `--doctor` для диагностики установки.

<!-- readme-unit:h.license -->
## Лицензия

<!-- readme-unit:license -->
SuperCli использует [лицензию MIT](../../LICENSE). Включённые зависимости и содержимое имеют собственные уведомления; см. [уведомления третьих сторон](../../THIRD_PARTY_NOTICES.md) и [руководство по навыкам](../builtin-skills.md).
