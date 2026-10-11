# Preserved project notes — 2026-10-11

Two empty text files in the repository root represented these outstanding issues.
Their exact names and meaning are preserved here; the empty originals were moved
to the portable local validation directory, not discarded.

- `czasami supercli gui włacza się z 7 kartami webview a czasami bez nich ogarnij to.txt`:
  investigate inconsistent restoration/opening of GUI WebView tabs. This pass
  does not establish the cause or claim to fix tab restoration.
- `w checkpoints tworzyła się duża baza ogarnij to.txt`:
  inspect checkpoint disk usage without silently removing recoverable history.
  The retention audit and conservative cleanup are documented in
  [the finalization report](../evals/2026-10-11-reliability-finalization.md).

The old literal `%SystemDrive%` directory was also moved out of the repository
root into the same portable validation directory. It contained six old Windows
cache files, totaling 957,024 bytes, last modified on 2026-10-01. This is a
quarantine, not disk-space reclamation or proof of the original producer. Keep
the ignore/search exclusion as a defense until that producer is identified.
