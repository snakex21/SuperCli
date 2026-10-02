[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.2

<!-- readme-unit:intro -->
Un agente de programación con IA portátil escrito en Go, con interfaz de terminal (TUI), interfaz de escritorio/web (GUI) y modo por lotes que comparten un motor.

<!-- readme-unit:status -->
Este README describe la versión `1.0.2`. Los paquetes portátiles se distribuyen mediante [GitHub Releases](https://github.com/snakex21/SuperCli/releases); una compilación local no implica que su versión ya se haya publicado.

<!-- readme-unit:h.screenshots -->
## Capturas de pantalla

<!-- readme-unit:screenshot.gui -->
![Interfaz web de SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centro de acciones del terminal de SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
La imagen de la GUI es una captura de pantalla; la imagen de la TUI es una representación de la disposición real del terminal de la aplicación. [Más capturas de pantalla](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Primeros pasos

<!-- readme-unit:start -->
Extraiga el paquete de su sistema operativo y arquitectura en una carpeta con permisos de escritura. En Windows, inicie `supercli.exe` para el terminal o `supercli-web.exe` para la GUI. Mantenga la carpeta incluida `supercli-data/` junto a los ejecutables.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
En Linux o macOS, utilice el ejecutable correspondiente del paquete o compile desde el código fuente. Seleccione su proyecto con `--home`; use `--batch` para una consulta sin la TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Datos portátiles

<!-- readme-unit:data -->
Configuración, sesiones, memoria, credenciales, cachés, registros y copias de seguridad residen en `supercli-data/` junto a la aplicación. Mueva toda la carpeta de la aplicación para llevarlos consigo. La aplicación no usa `%APPDATA%`, `%LOCALAPPDATA%` ni el registro de Windows para su estado.

<!-- readme-unit:workspace -->
`--home` y `SUPERCLI_HOME` seleccionan el espacio de trabajo sin mover los datos de la aplicación. Los ajustes del proyecto y sus archivos de trabajo usan `<project>/.supercli/`.

<!-- readme-unit:override -->
Solo `--data-dir` o `SUPERCLI_DATA_DIR` explícitos cambian la ubicación de los datos. Si la carpeta no permite escritura, el inicio informa de un error en vez de usar silenciosamente una carpeta del perfil.

<!-- readme-unit:legacy -->
En el primer inicio, el terminal puede copiar datos antiguos de `~/.supercli` a un directorio portátil vacío, conservando el original. Consulte la [distribución de datos](../data-layout.md).

<!-- readme-unit:secrets -->
Las credenciales viajan con la carpeta portátil. Manténgala privada, haga copias de seguridad y nunca incorpore claves API ni archivos de autenticación a un repositorio público.

<!-- readme-unit:h.config -->
## Modelos y configuración

<!-- readme-unit:providers -->
Configure proveedores desde los ajustes de la GUI o TUI `/providers` y `/models`. Se admiten endpoints compatibles con OpenAI, Anthropic nativo, ChatGPT/Codex OAuth, pasarelas opencode y un proveedor echo sin conexión.

<!-- readme-unit:config -->
Los ajustes globales están en `supercli-data/config.toml`; `<project>/.supercli/config.toml` puede sobrescribirlos. Las variables de entorno y opciones CLI tienen prioridad. Ejemplo de endpoint local:

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
Sustituya el endpoint y modelo de ejemplo por los valores de su servidor. Los servicios en la nube pueden exigir credenciales y cobrar por el uso. Las capacidades del modelo determinan visión, herramientas, razonamiento y límites de contexto. Consulte [configuración](../configuration.md).

<!-- readme-unit:h.features -->
## Funciones

<!-- readme-unit:surface -->
GUI y TUI admiten los mismos 27 idiomas de interfaz. Ofrecen conversaciones en streaming, recuperación de sesiones, selección de proyectos, gestión de modelos, adjuntos y vistas de uso. La TUI admite desplazamiento con ratón, búsqueda en la conversación y salida de razonamiento/herramientas plegable.

<!-- readme-unit:agent -->
El motor admite descubrimiento de herramientas, ediciones verificadas de archivos, ejecución de comandos, historial de sesiones, memoria de proyectos, compactación del contexto, objetivos, delegación a agentes trabajadores, consultas y modelos de borrador opcionales.

<!-- readme-unit:tools -->
Las herramientas cubren búsqueda de código, lecturas y parches específicos de archivos, imágenes, archivos ZIP, documentos DOCX/XLSX/PDF y ejecución de contexto limitada. Las herramientas disponibles dependen del perfil y modelo elegidos; use el descubrimiento de herramientas para el catálogo actual.

<!-- readme-unit:extensions -->
Los paquetes MCP opcionales están en `supercli-data/mcp/` y arrancan al usarse. El archivo de habilidades integradas está en `supercli-data/skills/builtin-skills.zip`; su ausencia no impide el inicio normal. Las extensiones pueden necesitar sus propios entornos de ejecución o aplicaciones anfitrionas instaladas.

<!-- readme-unit:optional -->
La generación de candidatos Darwin, los consejos y la delegación a trabajadores son flujos opcionales. Las llamadas paralelas a modelos pueden aumentar el consumo de recursos y el coste. El ejecutable principal no requiere Node, Python, Docker ni CGO.

<!-- readme-unit:h.controls -->
## Comandos y controles

<!-- readme-unit:controls -->
Escriba `/` para abrir la paleta de comandos. Abra el centro de acciones de la TUI con `Tab` en una entrada vacía o `Ctrl+K`; seleccione acciones con las flechas y vuelva con `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Comando | Acción |
| --- | --- |
| `/help`, `/doctor` | Mostrar ayuda y diagnóstico. |
| `/model`, `/models`, `/providers` | Elegir modelos y gestionar proveedores. |
| `/resume`, `/export` | Reanudar o exportar una conversación. |
| `/status`, `/cost`, `/compact` | Consultar el uso o compactar el contexto. |
| `/goal`, `/plan` | Gestionar objetivos y el modo de planificación de solo lectura. |
| `/diff`, `/undo`, `/redo` | Inspeccionar cambios o deshacer/rehacer un turno del agente. |
| `/darwin`, `/council` | Usar flujos opcionales con varios modelos. |
| `/update check\|download\|install` | Buscar, descargar o instalar explícitamente una actualización. |
| `/quit`, `/exit` | Salir de la aplicación. |

<!-- readme-unit:keys -->
`Enter` envía una consulta; `Ctrl+C` interrumpe. `PgUp`/`PgDn` y la rueda del ratón desplazan; `Ctrl+F` busca; `Shift+T` alterna el razonamiento; `Shift+E` amplía la salida de herramientas. Las consultas introducidas durante una ejecución se encolan para el siguiente paso seguro.

<!-- readme-unit:h.updates -->
## Actualizaciones

<!-- readme-unit:update.check -->
Busque versiones estables de GitHub bajo demanda desde la pantalla About de la GUI, TUI `/update check` o CLI `--check-update`. La comprobación no instala nada.

<!-- readme-unit:update.download -->
La descarga prepara el paquete portátil correspondiente y verifica su SHA256 contra el manifiesto de la versión. La descarga de la GUI y `/update download` mantienen la instalación en ejecución.

<!-- readme-unit:update.install -->
Use el botón de instalación de la GUI o `/update install` para instalar la actualización preparada. CLI `--update` solicita explícitamente descarga e instalación. Se conserva `supercli-data/` y se crean copias locales de los ejecutables sustituidos.

<!-- readme-unit:update.restart -->
Reinicie manualmente tras instalar. Cierre otras copias antes de instalar; Windows puede bloquear los ejecutables en uso. Si falta un paquete adecuado o sus metadatos de verificación, use una descarga manual verificada de la versión. Mantenga su propia copia de seguridad de los datos.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Documentación

<!-- readme-unit:docs.start -->
Empiece por el [índice de documentación](../README.md), [inicio rápido](../quickstart.md), [distribución de datos](../data-layout.md) y [configuración](../configuration.md).

<!-- readme-unit:docs.engine -->
Lea [arquitectura](../architecture.md), [delegación](../delegation.md), [rendimiento](../performance.md), [diseño de GUI](../webgui.md) y [estructura del proyecto](../project-structure.md) para conocer los detalles de implementación.

<!-- readme-unit:docs.extra -->
Guías de funciones: [MCP portátil](../portable-mcp.md), [habilidades integradas](../builtin-skills.md) y [telemetría](../telemetry.md). Seguimiento del desarrollo: [plan](../PLAN.md) y [hoja de ruta](../ROADMAP.md).

<!-- readme-unit:reference -->
El [README completo anterior](../readme-reference.md) se conserva como referencia histórica; las descripciones antiguas pueden ser anteriores a la versión `1.0.0`.

<!-- readme-unit:h.build -->
## Compilar y verificar

<!-- readme-unit:build -->
Use la versión de Go indicada en `go.mod`. En Windows, `build.bat` compila la TUI, `build_ui.bat` la GUI y `run.bat` compila cuando es necesario e inicia el terminal.

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
Ejecute las pruebas de Go antes de una versión. El verificador de documentación comprueba las 27 traducciones, el contenido asignado, encabezados, enlaces, literales técnicos y ejemplos de código idénticos.

<!-- readme-unit:requirements -->
El uso normal requiere un endpoint de modelo configurado o una cuenta. Git y ripgrep mejoran los flujos de repositorios; las herramientas externas son opcionales salvo que un flujo las necesite. La GUI requiere además soporte de webview/navegador de su plataforma. Use `--doctor` para diagnosticar la instalación.

<!-- readme-unit:h.license -->
## Licencia

<!-- readme-unit:license -->
SuperCli usa la [licencia MIT](../../LICENSE). Las dependencias y el contenido incluidos tienen sus propios avisos; consulte [avisos de terceros](../../THIRD_PARTY_NOTICES.md) y la [guía de habilidades](../builtin-skills.md).
