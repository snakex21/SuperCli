[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.2

<!-- readme-unit:intro -->
Ein portabler KI-Programmieragent in Go mit Terminaloberfläche (TUI), Desktop-/Weboberfläche (GUI) und Stapelmodus, die dieselbe Engine nutzen.

<!-- readme-unit:status -->
Dieses README beschreibt Version `1.0.2`. Portable Veröffentlichungspakete werden über [GitHub Releases](https://github.com/snakex21/SuperCli/releases) verteilt; ein lokaler Build bedeutet nicht, dass seine Veröffentlichung bereits erfolgt ist.

<!-- readme-unit:h.screenshots -->
## Screenshots

<!-- readme-unit:screenshot.gui -->
![SuperCli-Weboberfläche (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Aktionszentrum im SuperCli-Terminal (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Das GUI-Bild ist ein Screenshot; das TUI-Bild ist eine Darstellung des tatsächlichen Terminallayouts der Anwendung. [Weitere Screenshots](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Erste Schritte

<!-- readme-unit:start -->
Entpacken Sie das Paket für Ihr Betriebssystem und Ihre Architektur in einen beschreibbaren Ordner. Starten Sie unter Windows `supercli.exe` für das Terminal oder `supercli-web.exe` für die GUI. Lassen Sie den mitgelieferten Ordner `supercli-data/` neben den ausführbaren Dateien.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Verwenden Sie unter Linux oder macOS die passende ausführbare Datei aus dem Paket oder bauen Sie aus dem Quellcode. Wählen Sie Ihr Projekt mit `--home`; verwenden Sie `--batch` für eine einzelne Anfrage ohne TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Portable Daten

<!-- readme-unit:data -->
Einstellungen, Sitzungen, Gedächtnis, Zugangsdaten, Caches, Protokolle und Sicherungen liegen in `supercli-data/` neben der Anwendung. Verschieben Sie den gesamten Anwendungsordner, um sie mitzunehmen. Die Anwendung speichert ihren Zustand weder in `%APPDATA%` noch in `%LOCALAPPDATA%` oder der Windows-Registrierung.

<!-- readme-unit:workspace -->
`--home` und `SUPERCLI_HOME` wählen den Arbeitsbereich, ohne Anwendungsdaten zu verschieben. Projektanpassungen und Arbeitsartefakte verwenden `<project>/.supercli/`.

<!-- readme-unit:override -->
Nur ein ausdrücklich gesetztes `--data-dir` oder `SUPERCLI_DATA_DIR` ändert den Datenpfad. Ist der Anwendungsordner nicht beschreibbar, meldet der Start einen Fehler, statt unbemerkt einen Profilordner zu verwenden.

<!-- readme-unit:legacy -->
Das Terminal kann beim ersten Start ältere Daten aus `~/.supercli` in ein leeres portables Verzeichnis kopieren und das Original behalten. Siehe [Datenstruktur](../data-layout.md).

<!-- readme-unit:secrets -->
Zugangsdaten werden mit dem portablen Ordner übertragen. Halten Sie ihn privat, sichern Sie ihn und übernehmen Sie niemals API-Schlüssel oder Authentifizierungsdateien in ein öffentliches Repository.

<!-- readme-unit:h.config -->
## Modelle und Konfiguration

<!-- readme-unit:providers -->
Konfigurieren Sie Anbieter über die GUI-Einstellungen oder TUI `/providers` und `/models`. Unterstützt werden OpenAI-kompatible Endpunkte, natives Anthropic, ChatGPT/Codex OAuth, opencode-Gateways und ein Offline-Echo-Anbieter.

<!-- readme-unit:config -->
Globale Einstellungen stehen in `supercli-data/config.toml`; `<project>/.supercli/config.toml` kann sie überschreiben. Umgebungsvariablen und CLI-Optionen haben Vorrang. Beispiel eines lokalen Endpunkts:

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
Ersetzen Sie Beispielendpunkt und Modell durch die Werte Ihres Servers. Cloud-Dienste können Zugangsdaten verlangen und Nutzungskosten berechnen. Modellfähigkeiten bestimmen Bildverständnis, Werkzeuge, Schlussfolgerungen und Kontextgrenzen. Siehe [Konfiguration](../configuration.md).

<!-- readme-unit:h.features -->
## Funktionen

<!-- readme-unit:surface -->
GUI und TUI unterstützen dieselben 27 Oberflächensprachen. Sie bieten gestreamte Gespräche, Sitzungswiederherstellung, Projektauswahl, Modellverwaltung, Anhänge und Nutzungsansichten. Die TUI unterstützt Mausscrollen, Gesprächssuche und einklappbare Denk-/Werkzeugausgaben.

<!-- readme-unit:agent -->
Die Engine unterstützt Werkzeugsuche, überprüfte Dateiänderungen, Befehlsausführung, Sitzungsverlauf, Projektgedächtnis, Kontextverdichtung, Ziele, Delegation an Arbeitsagenten, Konsultation und optionale Entwurfsmodelle.

<!-- readme-unit:tools -->
Werkzeuge umfassen Codesuche, gezieltes Lesen und Ändern von Dateien, Bilder, ZIP-Archive, DOCX/XLSX/PDF-Dokumente und begrenzte Kontextausführung. Verfügbare Werkzeuge hängen vom gewählten Profil und Modell ab; verwenden Sie die Werkzeugsuche für den aktuellen Katalog.

<!-- readme-unit:extensions -->
Optionale MCP-Pakete liegen in `supercli-data/mcp/` und starten bei Verwendung. Das Archiv eingebauter Fähigkeiten liegt in `supercli-data/skills/builtin-skills.zip`; sein Fehlen verhindert den normalen Start nicht. Erweiterungen können eigene Laufzeitumgebungen oder installierte Hostanwendungen benötigen.

<!-- readme-unit:optional -->
Darwin-Kandidatenerzeugung, Modellräte und Delegation an Arbeitsagenten sind optionale Abläufe. Parallele Modellaufrufe können Ressourcenverbrauch und Kosten erhöhen. Das zentrale Programm benötigt weder Node noch Python, Docker oder CGO.

<!-- readme-unit:h.controls -->
## Befehle und Bedienung

<!-- readme-unit:controls -->
Geben Sie `/` für die Befehlspalette ein. Öffnen Sie die TUI-Aktionszentrale mit `Tab` bei leerer Eingabe oder `Ctrl+K`; wählen Sie mit den Pfeiltasten und kehren Sie mit `Esc` zurück.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Befehl | Aktion |
| --- | --- |
| `/help`, `/doctor` | Hilfe und Diagnose anzeigen. |
| `/model`, `/models`, `/providers` | Modelle wählen und Anbieter verwalten. |
| `/resume`, `/export` | Ein Gespräch fortsetzen oder exportieren. |
| `/status`, `/cost`, `/compact` | Nutzung prüfen oder Kontext verdichten. |
| `/goal`, `/plan` | Ziele und schreibgeschützten Planmodus verwalten. |
| `/diff`, `/undo`, `/redo` | Änderungen prüfen oder einen Agentenschritt rückgängig machen/wiederholen. |
| `/darwin`, `/council` | Optionale Abläufe mit mehreren Modellen verwenden. |
| `/update check\|download\|install` | Ein Update prüfen, herunterladen oder ausdrücklich installieren. |
| `/quit`, `/exit` | Die Anwendung beenden. |

<!-- readme-unit:keys -->
`Enter` sendet eine Anfrage; `Ctrl+C` unterbricht. `PgUp`/`PgDn` und das Mausrad scrollen; `Ctrl+F` sucht; `Shift+T` schaltet die Denkanzeige um; `Shift+E` erweitert Werkzeugausgaben. Während eines Laufs eingegebene Anfragen werden für einen sicheren nächsten Schritt vorgemerkt.

<!-- readme-unit:h.updates -->
## Updates

<!-- readme-unit:update.check -->
Prüfen Sie stabile GitHub-Veröffentlichungen bei Bedarf über den GUI-Bildschirm About, TUI `/update check` oder CLI `--check-update`. Die Prüfung installiert nichts.

<!-- readme-unit:update.download -->
Der Download stellt das passende portable Paket bereit und prüft seinen SHA256 gegen das Veröffentlichungsmanifest. GUI-Download und `/update download` lassen die laufende Installation bestehen.

<!-- readme-unit:update.install -->
Installieren Sie das bereitgestellte Update über die GUI-Schaltfläche oder `/update install`. CLI `--update` fordert ausdrücklich Download und Installation an. Vorhandenes `supercli-data/` bleibt erhalten; ersetzte ausführbare Dateien werden lokal gesichert.

<!-- readme-unit:update.restart -->
Starten Sie nach der Installation manuell neu. Schließen Sie vorher andere Kopien; laufende ausführbare Dateien können unter Windows gesperrt sein. Fehlt ein passendes Paket oder Prüfmetadaten, verwenden Sie einen geprüften manuellen Download der Veröffentlichung. Behalten Sie eine eigene Datensicherung.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Dokumentation

<!-- readme-unit:docs.start -->
Beginnen Sie mit [Dokumentationsindex](../README.md), [Schnellstart](../quickstart.md), [Datenstruktur](../data-layout.md) und [Konfiguration](../configuration.md).

<!-- readme-unit:docs.engine -->
Details zur Implementierung finden Sie unter [Architektur](../architecture.md), [Delegation](../delegation.md), [Leistung](../performance.md), [GUI-Design](../webgui.md) und [Projektstruktur](../project-structure.md).

<!-- readme-unit:docs.extra -->
Funktionsanleitungen: [portables MCP](../portable-mcp.md), [eingebaute Fähigkeiten](../builtin-skills.md) und [Telemetrie](../telemetry.md). Entwicklungsverfolgung: [Plan](../PLAN.md) und [Roadmap](../ROADMAP.md).

<!-- readme-unit:reference -->
Das [vorherige vollständige README](../readme-reference.md) bleibt als historische Referenz erhalten; ältere Funktionsbeschreibungen können Version `1.0.0` vorausgehen.

<!-- readme-unit:h.build -->
## Bauen und überprüfen

<!-- readme-unit:build -->
Verwenden Sie die in `go.mod` angegebene Go-Version. Unter Windows baut `build.bat` die TUI, `build_ui.bat` die GUI; `run.bat` baut bei Bedarf und startet das Terminal.

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
Führen Sie vor einer Veröffentlichung die Go-Tests aus. Die Dokumentationsprüfung überprüft alle 27 Übersetzungen, zugeordnete Inhalte, Überschriften, Links, technische Literale und identische Codebeispiele.

<!-- readme-unit:requirements -->
Für den normalen Einsatz wird ein konfigurierter Modellendpunkt oder ein Konto benötigt. Git und ripgrep verbessern Repository-Abläufe; externe Werkzeuge sind optional, sofern ein Ablauf sie nicht benötigt. Die GUI benötigt zusätzlich Webview-/Browser-Unterstützung der Plattform. Diagnostizieren Sie Ihre Installation mit `--doctor`.

<!-- readme-unit:h.license -->
## Lizenz

<!-- readme-unit:license -->
SuperCli verwendet die [MIT-Lizenz](../../LICENSE). Mitgelieferte Abhängigkeiten und Inhalte haben eigene Hinweise; siehe [Drittanbieterhinweise](../../THIRD_PARTY_NOTICES.md) und [Fähigkeitenanleitung](../builtin-skills.md).
