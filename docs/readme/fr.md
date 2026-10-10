[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.5

<!-- readme-unit:intro -->
Un agent de programmation IA portable écrit en Go, avec une interface terminal (TUI), une interface bureau/web (GUI) et un mode traitement par lots partageant le même moteur.

<!-- readme-unit:status -->
Ce README décrit la version `1.0.5`. Les paquets portables sont distribués via [GitHub Releases](https://github.com/snakex21/SuperCli/releases) ; une compilation locale ne signifie pas que sa version a déjà été publiée.

<!-- readme-unit:h.screenshots -->
## Captures d’écran

<!-- readme-unit:screenshot.gui -->
![Interface web de SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Centre d’actions du terminal de SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
L’image de la GUI est une capture d’écran ; l’image de la TUI est un rendu de la disposition réelle du terminal de l’application. [Autres captures d’écran](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Prise en main

<!-- readme-unit:start -->
Extrayez le paquet correspondant à votre système et architecture dans un dossier accessible en écriture. Sous Windows, lancez `supercli.exe` pour le terminal ou `supercli-web.exe` pour la GUI. Conservez le dossier fourni `supercli-data/` à côté des exécutables.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Sous Linux ou macOS, utilisez l'exécutable correspondant du paquet ou compilez les sources. Sélectionnez votre projet avec `--home` ; utilisez `--batch` pour une requête sans TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Données portables

<!-- readme-unit:data -->
Paramètres, sessions, mémoire, identifiants, caches, journaux et sauvegardes se trouvent dans `supercli-data/` à côté de l'application. Déplacez tout le dossier de l'application pour les emporter. L'application n'utilise ni `%APPDATA%`, ni `%LOCALAPPDATA%`, ni le registre Windows pour son état.

<!-- readme-unit:workspace -->
`--home` et `SUPERCLI_HOME` sélectionnent l'espace de travail sans déplacer les données de l'application. Les paramètres propres au projet et les fichiers de travail utilisent `<project>/.supercli/`.

<!-- readme-unit:override -->
Seuls `--data-dir` ou `SUPERCLI_DATA_DIR` explicitement définis changent l'emplacement des données. Si le dossier de l'application n'est pas accessible en écriture, le démarrage signale une erreur au lieu d'utiliser discrètement un dossier du profil.

<!-- readme-unit:legacy -->
Au premier démarrage, le terminal peut copier les anciennes données de `~/.supercli` dans un répertoire portable vide en conservant l'original. Voir [organisation des données](../data-layout.md).

<!-- readme-unit:secrets -->
Les identifiants voyagent avec le dossier portable. Gardez-le privé, sauvegardez-le et ne versionnez jamais de clés API ou de fichiers d'authentification dans un dépôt public.

<!-- readme-unit:h.config -->
## Modèles et configuration

<!-- readme-unit:providers -->
Configurez les fournisseurs dans les paramètres GUI ou avec TUI `/providers` et `/models`. Les connexions prises en charge comprennent les points d'accès compatibles OpenAI, Anthropic natif, ChatGPT/Codex OAuth, les passerelles opencode et un fournisseur echo hors ligne.

<!-- readme-unit:config -->
Les paramètres globaux sont dans `supercli-data/config.toml` ; `<project>/.supercli/config.toml` peut les remplacer. Les variables d'environnement et options CLI sont prioritaires. Exemple de point d'accès local :

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
Remplacez le point d'accès et le modèle d'exemple par les valeurs de votre serveur. Les services cloud peuvent demander des identifiants et facturer l'utilisation. Les capacités du modèle déterminent vision, outils, raisonnement et limites de contexte. Voir [configuration](../configuration.md).

<!-- readme-unit:h.features -->
## Fonctionnalités

<!-- readme-unit:surface -->
GUI et TUI prennent en charge les mêmes 27 langues d'interface. Elles proposent conversations en streaming, récupération de sessions, sélection de projets, gestion des modèles, pièces jointes et vues d'utilisation. La TUI permet le défilement à la souris, la recherche dans la conversation et le repli des sorties de raisonnement/outils.

<!-- readme-unit:agent -->
Le moteur prend en charge la découverte d'outils, les modifications de fichiers vérifiées, l'exécution de commandes, l'historique des sessions, la mémoire de projet, la compression du contexte, les objectifs, la délégation à des agents de travail, la consultation et des modèles de brouillon facultatifs.

<!-- readme-unit:tools -->
Les outils couvrent recherche de code, lectures et correctifs ciblés, images, archives ZIP, documents DOCX/XLSX/PDF et exécution de contexte limitée. Les outils disponibles dépendent du profil et du modèle choisis ; utilisez la découverte d'outils pour le catalogue actuel.

<!-- readme-unit:extensions -->
Les paquets MCP facultatifs se trouvent dans `supercli-data/mcp/` et démarrent à l'utilisation. L'archive des compétences intégrées est dans `supercli-data/skills/builtin-skills.zip` ; son absence n'empêche pas un démarrage normal. Les extensions peuvent nécessiter leurs propres environnements d'exécution ou des applications hôtes installées.

<!-- readme-unit:optional -->
La génération de candidats Darwin, les conseils et la délégation à des agents de travail sont facultatifs. Les appels parallèles aux modèles peuvent accroître la consommation de ressources et le coût. L'exécutable principal ne nécessite ni Node, ni Python, ni Docker, ni CGO.

<!-- readme-unit:h.controls -->
## Commandes et contrôles

<!-- readme-unit:controls -->
Saisissez `/` pour la palette de commandes. Ouvrez le centre d'actions TUI avec `Tab` lorsque la saisie est vide ou `Ctrl+K` ; sélectionnez avec les flèches et revenez avec `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Commande | Action |
| --- | --- |
| `/help`, `/doctor` | Afficher l'aide et les diagnostics. |
| `/model`, `/models`, `/providers` | Choisir les modèles et gérer les fournisseurs. |
| `/resume`, `/export` | Reprendre ou exporter une conversation. |
| `/status`, `/cost`, `/compact` | Examiner l'utilisation ou compresser le contexte. |
| `/goal`, `/plan` | Gérer les objectifs et le mode plan en lecture seule. |
| `/diff`, `/undo`, `/redo` | Examiner les changements ou annuler/rétablir un tour d'agent. |
| `/darwin`, `/council` | Utiliser les workflows facultatifs à plusieurs modèles. |
| `/update check\|download\|install` | Vérifier, télécharger ou installer explicitement une mise à jour. |
| `/quit`, `/exit` | Quitter l'application. |

<!-- readme-unit:keys -->
`Enter` envoie une requête ; `Ctrl+C` interrompt. `PgUp`/`PgDn` et la molette font défiler ; `Ctrl+F` recherche ; `Shift+T` bascule le raisonnement ; `Shift+E` développe la sortie des outils. Les requêtes saisies pendant une exécution attendent en file le prochain point sûr.

<!-- readme-unit:h.updates -->
## Mises à jour

<!-- readme-unit:update.check -->
Vérifiez les versions stables GitHub à la demande depuis l'écran About de la GUI, TUI `/update check` ou CLI `--check-update`. La vérification n'installe rien.

<!-- readme-unit:update.download -->
Le téléchargement prépare le paquet portable correspondant et vérifie son SHA256 avec le manifeste de publication. Le téléchargement GUI et `/update download` laissent l'installation en cours en place.

<!-- readme-unit:update.install -->
Utilisez le bouton d'installation GUI ou `/update install` pour installer la mise à jour préparée. CLI `--update` demande explicitement téléchargement et installation. Le `supercli-data/` existant est conservé et les exécutables remplacés sont sauvegardés localement.

<!-- readme-unit:update.restart -->
Redémarrez manuellement après installation. Fermez les autres copies avant l'installation ; Windows peut verrouiller les exécutables en cours. Si aucun paquet correspondant ou aucune métadonnée de vérification n'est disponible, utilisez un téléchargement manuel vérifié de la version. Gardez votre propre sauvegarde des données.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Documentation

<!-- readme-unit:docs.start -->
Commencez par [l'index de documentation](../README.md), le [démarrage rapide](../quickstart.md), [l'organisation des données](../data-layout.md) et la [configuration](../configuration.md).

<!-- readme-unit:docs.engine -->
Lisez [architecture](../architecture.md), [délégation](../delegation.md), [performances](../performance.md), [conception GUI](../webgui.md) et [structure du projet](../project-structure.md) pour les détails d'implémentation.

<!-- readme-unit:docs.extra -->
Guides fonctionnels : [MCP portable](../portable-mcp.md), [compétences intégrées](../builtin-skills.md) et [télémétrie](../telemetry.md). Suivi du développement : [plan](../PLAN.md) et [feuille de route](../ROADMAP.md).

<!-- readme-unit:reference -->
Le [précédent README complet](../readme-reference.md) est conservé comme référence historique ; certaines anciennes descriptions peuvent précéder la version `1.0.0`.

<!-- readme-unit:h.build -->
## Compiler et vérifier

<!-- readme-unit:build -->
Utilisez la version de Go indiquée dans `go.mod`. Sous Windows, `build.bat` compile la TUI, `build_ui.bat` la GUI et `run.bat` compile si nécessaire puis lance le terminal.

[docs/releasing.md](../releasing.md)

```bash
npm ci --ignore-scripts --no-audit --no-fund
go test ./...
go build -o supercli ./cmd/supercli
go build -o supercli-web ./cmd/supercli-web
node docs/readme/check.cjs
```

```powershell
npm ci --ignore-scripts --no-audit --no-fund
go test ./...
go build -o supercli.exe ./cmd/supercli
go build -ldflags="-H windowsgui" -o supercli-web.exe ./cmd/supercli-web
node docs/readme/check.cjs
```

<!-- readme-unit:tests -->
Exécutez les tests Go avant une publication. Le vérificateur documentaire contrôle les 27 traductions, le contenu associé, les titres, liens, littéraux techniques et exemples de code identiques.

<!-- readme-unit:requirements -->
L'utilisation normale nécessite un point d'accès de modèle configuré ou un compte. Git et ripgrep améliorent les workflows de dépôt ; les outils externes sont facultatifs sauf si un workflow les exige. La GUI nécessite aussi la prise en charge webview/navigateur de la plateforme. Utilisez `--doctor` pour diagnostiquer l'installation.

<!-- readme-unit:h.license -->
## Licence

<!-- readme-unit:license -->
SuperCli utilise la [licence MIT](../../LICENSE). Les dépendances et contenus inclus ont leurs propres mentions ; voir les [mentions tierces](../../THIRD_PARTY_NOTICES.md) et le [guide des compétences](../builtin-skills.md).
