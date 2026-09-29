# Preparing and updating releases

The canonical source version is `internal/buildinfo/version.go`. Validate the Go suite, UI suite and all README translations before packaging:

```text
go test ./...
go vet ./...
node scripts/test-ui.cjs
node docs/readme/check.cjs
```

Build both binaries into a clean `bin/` directory. For Windows:

```powershell
go build -trimpath -buildvcs=false -o bin/supercli.exe ./cmd/supercli
go build -trimpath -buildvcs=false -ldflags="-H=windowsgui" -o bin/supercli-web.exe ./cmd/supercli-web
go run ./scripts/release -version 1.0.0 -os windows -arch amd64 -cli bin/supercli.exe -gui bin/supercli-web.exe -output dist
```

Use corresponding Linux binaries with `-os linux` and macOS binaries with `-os darwin`. Provide both `-arch amd64` and `-arch arm64` for Linux and macOS; Windows is packaged for amd64. On Linux/macOS the GUI uses a Chromium browser in app mode with a portable profile, or `--no-window` for the server only. Packaging fails if a ZIP already exists: use a fresh output directory. The tool includes only both executables, public documentation/license notices and the exact `supercli-data/skills/builtin-skills.zip` asset if present. It never copies settings, credentials, sessions, logs or the rest of `supercli-data/`.

Combine all platform ZIPs into one folder and run:

```text
go run ./scripts/release -manifest -version 1.0.0 -output dist
```

Upload the ZIPs, `supercli-update.json` and `SHA256SUMS` together to a stable GitHub release. The manifest names each platform, file, exact byte size and SHA256 digest. A release without a matching verified bundle remains a manual update. The first 1.0.0 release must already include the manifest so later versions can use it consistently.

The `Portable release` GitHub workflow builds Windows amd64, Linux amd64/arm64 and macOS amd64/arm64 bundles and creates a **draft** when an explicit version tag is pushed or it is manually invoked. Review the draft and smoke-test both platforms before publishing. Existing published tags are not silently overwritten. If a release already exists, the workflow leaves its assets intact. Release builds use `-buildvcs=false` so unrelated portable files beside the source do not affect VCS metadata.

## Installed application

GUI About: check → download → install. TUI: `/update check`, `/update download`, then `/update install`. CLI `--check-update` checks only; `--update` explicitly downloads and installs. No startup checks or background update polling are added.

Metadata is fetched over HTTPS from the project's GitHub releases. Each bundle's SHA256 and size are checked before extraction, staged binaries are checked again before installation, and both replaced executables are backed up in `supercli-updates/backup-VERSION-*/`. A failed pair replacement attempts to restore both old files; diagnostics identify the backup directory if restoration needs manual recovery. Private portable application data is never replaced.

Close other copies before installing. Restart manually after a successful installation. Checks and failed downloads do not stop an active agent; installation in GUI/TUI is refused until foreground work and workers finish. If the installation folder is unwritable or files are locked, the update reports an error and never redirects its files into a profile/system directory.

SHA256 protects against corrupt or mismatched assets; it does not provide an independent signing trust root. Release access and GitHub transport are part of the update trust boundary. Keep normal backups of portable data.
