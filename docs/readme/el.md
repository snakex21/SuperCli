[English](en.md) · [Български](bg.md) · [Čeština](cs.md) · [Dansk](da.md) · [Deutsch](de.md) · [Ελληνικά](el.md) · [Español](es.md) · [Eesti](et.md) · [Suomi](fi.md) · [Français](fr.md) · [Hrvatski](hr.md) · [Magyar](hu.md) · [Italiano](it.md) · [Lietuvių](lt.md) · [Latviešu](lv.md) · [Norsk bokmål](nb.md) · [Nederlands](nl.md) · [Polski](pl.md) · [Português (Brasil)](pt-BR.md) · [Română](ro.md) · [Русский](ru.md) · [Slovenčina](sk.md) · [Slovenščina](sl.md) · [Srpski (latinica)](sr-Latn.md) · [Svenska](sv.md) · [Türkçe](tr.md) · [Українська](uk.md)

<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->

# SuperCli 1.0.5

<!-- readme-unit:intro -->
Ένας φορητός πράκτορας AI για προγραμματισμό, γραμμένος σε Go, με διεπαφή τερματικού (TUI), επιτραπέζια/διαδικτυακή διεπαφή (GUI) και λειτουργία δέσμης που μοιράζονται μία μηχανή.

<!-- readme-unit:status -->
Αυτό το README περιγράφει την έκδοση `1.0.5`. Τα φορητά πακέτα διανέμονται μέσω του [GitHub Releases](https://github.com/snakex21/SuperCli/releases)· μια τοπική μεταγλώττιση δεν σημαίνει ότι η έκδοσή της έχει ήδη δημοσιευτεί.

<!-- readme-unit:h.screenshots -->
## Στιγμιότυπα οθόνης

<!-- readme-unit:screenshot.gui -->
![Διαδικτυακό περιβάλλον του SuperCli (GUI)](../screenshots/1.0.0/gui-en.jpg)

<!-- readme-unit:screenshot.tui -->
![Κέντρο ενεργειών τερματικού του SuperCli (TUI)](../screenshots/1.0.0/tui-actions-en.jpg)

<!-- readme-unit:screenshots.more -->
Η εικόνα του GUI είναι στιγμιότυπο οθόνης· η εικόνα του TUI είναι απόδοση της πραγματικής διάταξης τερματικού της εφαρμογής. [Περισσότερα στιγμιότυπα](../screenshots/1.0.0/README.md).

<!-- readme-unit:h.start -->
## Ξεκινήστε

<!-- readme-unit:start -->
Αποσυμπιέστε το πακέτο για το λειτουργικό σύστημα και την αρχιτεκτονική σας σε εγγράψιμο φάκελο. Στα Windows, εκκινήστε το `supercli.exe` για το τερματικό ή το `supercli-web.exe` για το GUI. Κρατήστε τον συνοδευτικό φάκελο `supercli-data/` δίπλα στα εκτελέσιμα.

```powershell
.\supercli.exe
.\supercli-web.exe
.\supercli.exe --home "D:\projects\example"
.\supercli.exe --batch "Summarize this project"
.\supercli.exe --doctor
```

<!-- readme-unit:start.other -->
Σε Linux ή macOS, χρησιμοποιήστε το αντίστοιχο εκτελέσιμο του πακέτου ή μεταγλωττίστε από τον πηγαίο κώδικα. Επιλέξτε έργο με `--home`· χρησιμοποιήστε `--batch` για ένα αίτημα χωρίς TUI.

```bash
./supercli --home ./my-project
./supercli --batch "Summarize this project"
./supercli --doctor
```

<!-- readme-unit:h.data -->
## Φορητά δεδομένα

<!-- readme-unit:data -->
Ρυθμίσεις, συνεδρίες, μνήμη, διαπιστευτήρια, προσωρινές μνήμες, αρχεία καταγραφής και αντίγραφα ασφαλείας βρίσκονται στο `supercli-data/` δίπλα στην εφαρμογή. Μετακινήστε ολόκληρο τον φάκελο της εφαρμογής για να τα μεταφέρετε. Η εφαρμογή δεν χρησιμοποιεί `%APPDATA%`, `%LOCALAPPDATA%` ή το μητρώο Windows για την κατάστασή της.

<!-- readme-unit:workspace -->
Τα `--home` και `SUPERCLI_HOME` επιλέγουν τον χώρο εργασίας χωρίς μετακίνηση των δεδομένων της εφαρμογής. Οι προσαρμογές έργου και τα αρχεία εργασίας χρησιμοποιούν το `<project>/.supercli/`.

<!-- readme-unit:override -->
Μόνο ένα ρητό `--data-dir` ή `SUPERCLI_DATA_DIR` αλλάζει τη θέση δεδομένων. Αν ο φάκελος εφαρμογής δεν είναι εγγράψιμος, η εκκίνηση αναφέρει σφάλμα αντί να χρησιμοποιήσει σιωπηρά φάκελο προφίλ.

<!-- readme-unit:legacy -->
Το τερματικό μπορεί στην πρώτη εκκίνηση να αντιγράψει παλαιά δεδομένα από το `~/.supercli` σε έναν άδειο φορητό κατάλογο, διατηρώντας το πρωτότυπο. Δείτε τη [διάταξη δεδομένων](../data-layout.md).

<!-- readme-unit:secrets -->
Τα διαπιστευτήρια μεταφέρονται μαζί με τον φορητό φάκελο. Κρατήστε τον ιδιωτικό, δημιουργήστε αντίγραφα ασφαλείας και μην καταχωρίζετε ποτέ κλειδιά API ή αρχεία πιστοποίησης σε δημόσιο αποθετήριο.

<!-- readme-unit:h.config -->
## Μοντέλα και διαμόρφωση

<!-- readme-unit:providers -->
Διαμορφώστε παρόχους από τις ρυθμίσεις GUI ή τα TUI `/providers` και `/models`. Υποστηρίζονται τελικά σημεία συμβατά με OpenAI, εγγενές Anthropic, ChatGPT/Codex OAuth, πύλες opencode και πάροχος echo χωρίς σύνδεση.

<!-- readme-unit:config -->
Οι γενικές ρυθμίσεις είναι στο `supercli-data/config.toml`· το `<project>/.supercli/config.toml` μπορεί να τις αντικαταστήσει. Οι μεταβλητές περιβάλλοντος και οι επιλογές CLI έχουν προτεραιότητα. Παράδειγμα τοπικού τελικού σημείου:

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
Αντικαταστήστε το τελικό σημείο και το μοντέλο του παραδείγματος με τις τιμές του διακομιστή σας. Οι υπηρεσίες νέφους μπορεί να απαιτούν διαπιστευτήρια και να χρεώνουν τη χρήση. Οι δυνατότητες μοντέλου καθορίζουν όραση, εργαλεία, συλλογισμό και όρια περιβάλλοντος. Δείτε τη [διαμόρφωση](../configuration.md).

<!-- readme-unit:h.features -->
## Δυνατότητες

<!-- readme-unit:surface -->
GUI και TUI υποστηρίζουν τις ίδιες 27 γλώσσες διεπαφής. Παρέχουν συνομιλίες συνεχούς ροής, ανάκτηση συνεδριών, επιλογή έργου, διαχείριση μοντέλων, συνημμένα και προβολές χρήσης. Το TUI υποστηρίζει κύλιση με ποντίκι, αναζήτηση συνομιλίας και πτυσσόμενη έξοδο σκέψης/εργαλείων.

<!-- readme-unit:agent -->
Η μηχανή υποστηρίζει ανακάλυψη εργαλείων, επαληθευμένες αλλαγές αρχείων, εκτέλεση εντολών, ιστορικό συνεδριών, μνήμη έργου, συμπύκνωση περιβάλλοντος, στόχους, ανάθεση σε πράκτορες εργασίας, διαβούλευση και προαιρετικά μοντέλα προσχεδίων.

<!-- readme-unit:tools -->
Τα εργαλεία καλύπτουν αναζήτηση κώδικα, στοχευμένες αναγνώσεις και διορθώσεις αρχείων, εικόνες, αρχεία ZIP, έγγραφα DOCX/XLSX/PDF και περιορισμένη εκτέλεση περιβάλλοντος. Τα διαθέσιμα εργαλεία εξαρτώνται από το επιλεγμένο προφίλ και μοντέλο· χρησιμοποιήστε την ανακάλυψη εργαλείων για τον τρέχοντα κατάλογο.

<!-- readme-unit:extensions -->
Τα προαιρετικά πακέτα MCP βρίσκονται στο `supercli-data/mcp/` και ξεκινούν όταν χρησιμοποιούνται. Το αρχείο ενσωματωμένων δεξιοτήτων βρίσκεται στο `supercli-data/skills/builtin-skills.zip`· η απουσία του δεν εμποδίζει την κανονική εκκίνηση. Οι επεκτάσεις μπορεί να χρειάζονται δικά τους περιβάλλοντα εκτέλεσης ή εγκατεστημένες εφαρμογές φιλοξενίας.

<!-- readme-unit:optional -->
Η παραγωγή υποψηφίων Darwin, τα συμβούλια και η ανάθεση σε πράκτορες εργασίας είναι προαιρετικές ροές. Οι παράλληλες κλήσεις μοντέλων μπορούν να αυξήσουν πόρους και κόστος. Το βασικό εκτελέσιμο δεν απαιτεί Node, Python, Docker ή CGO.

<!-- readme-unit:h.controls -->
## Εντολές και χειρισμός

<!-- readme-unit:controls -->
Πληκτρολογήστε `/` για την παλέτα εντολών. Ανοίξτε το κέντρο ενεργειών TUI με `Tab` σε κενή είσοδο ή `Ctrl+K`· επιλέξτε ενέργειες με τα βέλη και επιστρέψτε με `Esc`.

<!-- readme-unit:table.command,table.action,cmd.help,cmd.models,cmd.sessions,cmd.context,cmd.goal,cmd.diff,cmd.parallel,cmd.update,cmd.quit -->
| Εντολή | Ενέργεια |
| --- | --- |
| `/help`, `/doctor` | Εμφάνιση βοήθειας και διαγνωστικών. |
| `/model`, `/models`, `/providers` | Επιλογή μοντέλων και διαχείριση παρόχων. |
| `/resume`, `/export` | Συνέχιση ή εξαγωγή συνομιλίας. |
| `/status`, `/cost`, `/compact` | Έλεγχος χρήσης ή συμπύκνωση περιβάλλοντος. |
| `/goal`, `/plan` | Διαχείριση στόχων και λειτουργίας σχεδίου μόνο για ανάγνωση. |
| `/diff`, `/undo`, `/redo` | Έλεγχος αλλαγών ή αναίρεση/επανάληψη βήματος πράκτορα. |
| `/darwin`, `/council` | Χρήση προαιρετικών ροών με πολλά μοντέλα. |
| `/update check\|download\|install` | Έλεγχος, λήψη ή ρητή εγκατάσταση ενημέρωσης. |
| `/quit`, `/exit` | Έξοδος από την εφαρμογή. |

<!-- readme-unit:keys -->
Το `Enter` στέλνει αίτημα· το `Ctrl+C` διακόπτει. Τα `PgUp`/`PgDn` και ο τροχός ποντικιού κυλούν· το `Ctrl+F` αναζητά· το `Shift+T` εναλλάσσει τη σκέψη· το `Shift+E` επεκτείνει την έξοδο εργαλείων. Αιτήματα που εισάγονται κατά την εκτέλεση μπαίνουν σε ουρά για ασφαλές επόμενο βήμα.

<!-- readme-unit:h.updates -->
## Ενημερώσεις

<!-- readme-unit:update.check -->
Ελέγξτε σταθερές εκδόσεις GitHub κατ' απαίτηση από την οθόνη About του GUI, το TUI `/update check` ή το CLI `--check-update`. Ο έλεγχος δεν εγκαθιστά τίποτα.

<!-- readme-unit:update.download -->
Η λήψη προετοιμάζει το αντίστοιχο φορητό πακέτο και επαληθεύει το SHA256 του με το manifest της έκδοσης. Η λήψη GUI και το `/update download` αφήνουν την τρέχουσα εγκατάσταση στη θέση της.

<!-- readme-unit:update.install -->
Χρησιμοποιήστε το κουμπί εγκατάστασης GUI ή το `/update install` για την προετοιμασμένη ενημέρωση. Το CLI `--update` ζητά ρητά λήψη και εγκατάσταση. Το υπάρχον `supercli-data/` διατηρείται και τα εκτελέσιμα που αντικαθίστανται αντιγράφονται σε τοπικά αντίγραφα ασφαλείας.

<!-- readme-unit:update.restart -->
Επανεκκινήστε χειροκίνητα μετά την εγκατάσταση. Κλείστε άλλα αντίγραφα πριν την εγκατάσταση· εκτελούμενα αρχεία μπορεί να είναι κλειδωμένα στα Windows. Αν δεν υπάρχει αντίστοιχο πακέτο ή μεταδεδομένα επαλήθευσης, χρησιμοποιήστε επαληθευμένη χειροκίνητη λήψη έκδοσης. Κρατήστε δικό σας αντίγραφο ασφαλείας δεδομένων.

```powershell
.\supercli.exe --check-update
.\supercli.exe --update
```

<!-- readme-unit:h.docs -->
## Τεκμηρίωση

<!-- readme-unit:docs.start -->
Ξεκινήστε από το [ευρετήριο τεκμηρίωσης](../README.md), τη [γρήγορη εκκίνηση](../quickstart.md), τη [διάταξη δεδομένων](../data-layout.md) και τη [διαμόρφωση](../configuration.md).

<!-- readme-unit:docs.engine -->
Διαβάστε [αρχιτεκτονική](../architecture.md), [ανάθεση](../delegation.md), [επιδόσεις](../performance.md), [σχεδιασμό GUI](../webgui.md) και [δομή έργου](../project-structure.md) για λεπτομέρειες υλοποίησης.

<!-- readme-unit:docs.extra -->
Οδηγοί λειτουργιών: [φορητό MCP](../portable-mcp.md), [ενσωματωμένες δεξιότητες](../builtin-skills.md) και [τηλεμετρία](../telemetry.md). Παρακολούθηση ανάπτυξης: [σχέδιο](../PLAN.md) και [οδικός χάρτης](../ROADMAP.md).

<!-- readme-unit:reference -->
Το [προηγούμενο πλήρες README](../readme-reference.md) διατηρείται ως ιστορική αναφορά· παλαιότερες περιγραφές λειτουργιών μπορεί να προηγούνται της έκδοσης `1.0.0`.

<!-- readme-unit:h.build -->
## Μεταγλώττιση και επαλήθευση

<!-- readme-unit:build -->
Χρησιμοποιήστε την έκδοση Go που ορίζει το `go.mod`. Στα Windows, το `build.bat` χτίζει το TUI, το `build_ui.bat` το GUI και το `run.bat` μεταγλωττίζει αν χρειάζεται και εκκινεί το τερματικό.

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
Εκτελέστε τις δοκιμές Go πριν από έκδοση. Ο ελεγκτής τεκμηρίωσης επαληθεύει και τις 27 μεταφράσεις, την αντιστοιχισμένη ύλη, επικεφαλίδες, συνδέσμους, τεχνικά λεκτικά και πανομοιότυπα παραδείγματα κώδικα.

<!-- readme-unit:requirements -->
Η κανονική χρήση απαιτεί διαμορφωμένο τελικό σημείο μοντέλου ή λογαριασμό. Git και ripgrep βελτιώνουν τις ροές αποθετηρίου· εξωτερικά εργαλεία είναι προαιρετικά εκτός αν απαιτούνται από τη ροή. Το GUI απαιτεί επιπλέον υποστήριξη webview/προγράμματος περιήγησης της πλατφόρμας. Χρησιμοποιήστε `--doctor` για διάγνωση εγκατάστασης.

<!-- readme-unit:h.license -->
## Άδεια

<!-- readme-unit:license -->
Το SuperCli χρησιμοποιεί την [άδεια MIT](../../LICENSE). Οι συνοδευτικές εξαρτήσεις και το περιεχόμενο έχουν δικές τους γνωστοποιήσεις· δείτε τις [γνωστοποιήσεις τρίτων](../../THIRD_PARTY_NOTICES.md) και τον [οδηγό δεξιοτήτων](../builtin-skills.md).
