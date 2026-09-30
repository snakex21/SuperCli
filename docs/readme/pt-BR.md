[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.1

<!-- readme-unit:intro -->
Um agente de programação com IA portátil escrito em Go, com interface de terminal (TUI), interface desktop/web (GUI) e modo em lote que compartilham um único mecanismo.

<!-- readme-unit:status -->
Este README descreve a versão `1.0.1`. Os pacotes portáteis são distribuídos pelo [GitHub Releases](https://github.com/snakex21/SuperCli/releases); uma compilação local não significa que sua versão já foi publicada.

<!-- readme-unit:h.screenshots -->
## Capturas de tela

<!-- readme-unit:screenshot.gui -->
![Interface web do SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Central de ações do terminal do SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
A imagem da GUI é uma captura de tela; a imagem da TUI é uma renderização do layout real do terminal do aplicativo. [Mais capturas de tela](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Primeiros passos

<!-- readme-unit:start -->
Extraia o pacote para seu sistema operacional e arquitetura em uma pasta com permissão de gravação. No Windows, execute `supercli.exe` para o terminal ou `supercli-web.exe` para a GUI. Mantenha a pasta incluída `supercli-data/` ao lado dos executáveis.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
No Linux ou macOS, use o executável correspondente do pacote ou compile a partir do código-fonte. Selecione seu projeto com `--home`; use `--batch` para uma solicitação sem TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Dados portáteis

<!-- readme-unit:data -->
Configurações, sessões, memória, credenciais, caches, logs e backups ficam em `supercli-data/` ao lado do aplicativo. Mova toda a pasta do aplicativo para levá-los com você. O aplicativo não usa `%APPDATA%`, `%LOCALAPPDATA%` ou o Registro do Windows para seu estado.

<!-- readme-unit:workspace -->
`--home` e `SUPERCLI_HOME` selecionam o espaço de trabalho sem mover os dados do aplicativo. As substituições de configuração do projeto e os arquivos de trabalho usam `<project>/.supercli/`.

<!-- readme-unit:override -->
Somente `--data-dir` ou `SUPERCLI_DATA_DIR` explícitos alteram o local dos dados. Se a pasta do aplicativo não permitir gravação, a inicialização informa um erro em vez de usar silenciosamente uma pasta de perfil.

<!-- readme-unit:legacy -->
Na primeira inicialização, o terminal pode copiar dados antigos de `~/.supercli` para um diretório portátil vazio, preservando o original. Veja [organização dos dados](../data-layout.md).

<!-- readme-unit:secrets -->
As credenciais acompanham a pasta portátil. Mantenha-a privada, faça backups e nunca inclua chaves API ou arquivos de autenticação em um repositório público.

<!-- readme-unit:h.config -->
## Modelos e configuração

<!-- readme-unit:providers -->
Configure provedores nas configurações da GUI ou por TUI `/providers` e `/models`. As conexões suportadas incluem endpoints compatíveis com OpenAI, Anthropic nativo, ChatGPT/Codex OAuth, gateways opencode e um provedor echo offline.

<!-- readme-unit:config -->
As configurações globais ficam em `supercli-data/config.toml`; `<project>/.supercli/config.toml` pode substituí-las. Variáveis de ambiente e opções CLI têm prioridade. Exemplo de endpoint local:

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
Substitua o endpoint e modelo do exemplo pelos valores do seu servidor. Serviços de nuvem podem exigir credenciais e cobrar pelo uso. As capacidades do modelo determinam visão, ferramentas, raciocínio e limites de contexto. Veja [configuração](../configuration.md).

<!-- readme-unit:h.features -->
## Recursos

<!-- readme-unit:surface -->
GUI e TUI suportam os mesmos 27 idiomas de interface. Oferecem conversas em streaming, recuperação de sessões, seleção de projetos, gerenciamento de modelos, anexos e visualizações de uso. A TUI suporta rolagem com mouse, busca na conversa e saída de raciocínio/ferramentas recolhível.

<!-- readme-unit:agent -->
O mecanismo suporta descoberta de ferramentas, edições verificadas de arquivos, execução de comandos, histórico de sessões, memória de projeto, compactação de contexto, metas, delegação a agentes de trabalho, consulta e modelos de rascunho opcionais.

<!-- readme-unit:tools -->
As ferramentas abrangem busca de código, leituras e patches direcionados de arquivos, imagens, arquivos ZIP, documentos DOCX/XLSX/PDF e execução de contexto limitada. As ferramentas disponíveis dependem do perfil e modelo escolhidos; use a descoberta de ferramentas para o catálogo atual.

<!-- readme-unit:extensions -->
Pacotes MCP opcionais ficam em `supercli-data/mcp/` e iniciam quando utilizados. O arquivo de habilidades integradas fica em `supercli-data/skills/builtin-skills.zip`; sua ausência não impede a inicialização normal. Extensões podem precisar de seus próprios runtimes ou aplicativos hospedeiros instalados.

<!-- readme-unit:optional -->
Geração de candidatos Darwin, conselhos e delegação a agentes de trabalho são fluxos opcionais. Chamadas paralelas a modelos podem aumentar o consumo de recursos e o custo. O executável principal não exige Node, Python, Docker ou CGO.

<!-- readme-unit:h.controls -->
## Comandos e controles

<!-- readme-unit:controls -->
Digite `/` para a paleta de comandos. Abra o centro de ações da TUI com `Tab` em uma entrada vazia ou `Ctrl+K`; selecione ações com as setas e volte com `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Comando | Ação |
| --- | --- |
| `/help`, `/doctor` | Mostrar ajuda e diagnóstico. |
| `/model`, `/models`, `/providers` | Escolher modelos e gerenciar provedores. |
| `/resume`, `/export` | Retomar ou exportar uma conversa. |
| `/status`, `/cost`, `/compact` | Examinar o uso ou compactar o contexto. |
| `/goal`, `/plan` | Gerenciar metas e modo de planejamento somente leitura. |
| `/diff`, `/undo`, `/redo` | Examinar alterações ou desfazer/refazer um turno do agente. |
| `/darwin`, `/council` | Usar fluxos opcionais com vários modelos. |
| `/update check\|download\|install` | Verificar, baixar ou instalar explicitamente uma atualização. |
| `/quit`, `/exit` | Sair do aplicativo. |

<!-- readme-unit:keys -->
`Enter` envia uma solicitação; `Ctrl+C` interrompe. `PgUp`/`PgDn` e a roda do mouse rolam; `Ctrl+F` busca; `Shift+T` alterna o raciocínio; `Shift+E` expande a saída das ferramentas. Solicitações digitadas durante uma execução entram na fila para o próximo passo seguro.

<!-- readme-unit:h.updates -->
## Atualizações

<!-- readme-unit:update.check -->
Verifique versões estáveis do GitHub sob demanda na tela About da GUI, por TUI `/update check` ou CLI `--check-update`. A verificação não instala nada.

<!-- readme-unit:update.download -->
O download prepara o pacote portátil correspondente e verifica seu SHA256 com o manifesto da versão. O download da GUI e `/update download` mantêm a instalação em execução no lugar.

<!-- readme-unit:update.install -->
Use o botão de instalação da GUI ou `/update install` para instalar a atualização preparada. CLI `--update` solicita explicitamente download e instalação. O `supercli-data/` existente é preservado, e os executáveis substituídos recebem backups locais.

<!-- readme-unit:update.restart -->
Reinicie manualmente após a instalação. Feche outras cópias antes de instalar; executáveis em uso podem estar bloqueados no Windows. Se o pacote correspondente ou os metadados de verificação estiverem indisponíveis, use um download manual verificado da versão. Mantenha seu próprio backup dos dados.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Documentação

<!-- readme-unit:docs.start -->
Comece pelo [índice da documentação](../README.md), [início rápido](../quickstart.md), [organização dos dados](../data-layout.md) e [configuração](../configuration.md).

<!-- readme-unit:docs.engine -->
Leia [arquitetura](../architecture.md), [delegação](../delegation.md), [desempenho](../performance.md), [design da GUI](../webgui.md) e [estrutura do projeto](../project-structure.md) para detalhes da implementação.

<!-- readme-unit:docs.extra -->
Guias de recursos: [MCP portátil](../portable-mcp.md), [habilidades integradas](../builtin-skills.md) e [telemetria](../telemetry.md). Acompanhamento do desenvolvimento: [plano](../PLAN.md) e [roteiro](../ROADMAP.md).

<!-- readme-unit:reference -->
O [README completo anterior](../readme-reference.md) é mantido como referência histórica; descrições antigas podem ser anteriores à versão `1.0.0`.

<!-- readme-unit:h.build -->
## Compilar e verificar

<!-- readme-unit:build -->
Use a versão Go especificada em `go.mod`. No Windows, `build.bat` compila a TUI, `build_ui.bat` a GUI e `run.bat` compila se necessário e inicia o terminal.

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
Execute os testes Go antes de uma versão. O verificador de documentação verifica todas as 27 traduções, conteúdo mapeado, títulos, links, literais técnicos e exemplos de código idênticos.

<!-- readme-unit:requirements -->
O uso normal exige um endpoint de modelo configurado ou uma conta. Git e ripgrep melhoram os fluxos de repositórios; ferramentas externas são opcionais, salvo quando um fluxo as exige. A GUI também exige suporte de webview/navegador da plataforma. Use `--doctor` para diagnosticar sua instalação.

<!-- readme-unit:h.license -->
## Licença

<!-- readme-unit:license -->
SuperCli usa a [licença MIT](../../LICENSE). Dependências e conteúdo incluídos têm seus próprios avisos; veja [avisos de terceiros](../../THIRD_PARTY_NOTICES.md) e o [guia de habilidades](../builtin-skills.md).
