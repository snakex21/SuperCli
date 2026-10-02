# Application window screenshots

Use the target application rather than the currently visible monitor when demonstrating a program built or tested by the agent. Windows capture does not activate, restore or minimize a window. There is no fallback to the foreground screen.

Start the GUI executable directly with `process_session`:

```json
{"action":"start","command":["C:/project/build/demo.exe"]}
```

Keep the returned process session ID, then capture its window:

```json
{"action":"screenshot","id":"proc-1"}
```

If the process has multiple application windows, add `window_title` to select one within that same process. Shell descendants and launchers that exit after handing work to another process are not inferred. Use an explicit existing window selector for those applications. A stopped, exited or unknown owned session cannot capture another application. The native helper holds and verifies the process owner through capture.

For an already running application, `send_screenshot` accepts `source:"window"` with `process_id`, `window_title` or `window_id`. A process ID can be narrowed by title or handle; ambiguous targets return available choices. Request `source:"windows"` only when the target is unknown. Windows input-method/tool helper windows are excluded from automatic process selection; explicit title/handle selection remains possible.

For the Windows desktop wallpaper and icons, even while another application covers them:

```json
{"source":"desktop"}
```

This renders the shell's desktop window directly. It does not minimize applications, activate the desktop, move Explorer windows or include foreground applications. A split shell layout that puts the icons on a separate WorkerW surface returns an honest unsupported-layout error; it never substitutes a screen capture. Desktop capture does not accept application window selectors. Other operating systems currently report that desktop-only capture is unsupported.

The separate visible-screen option remains available:

```json
{"source":"screen"}
```

This captures what the user currently sees, including foreground applications. Use it only when that is the requested subject. Omitting source and all window selectors retains the legacy clipboard behavior.

A successful capture saves the original in the portable application data folder under `.supercli/snapshots`, and automatically displays a lazy thumbnail in live and restored GUI chat. Opening the thumbnail loads the original. TUI receives the same result and file path. `attach` defaults to false for desktop/screen/window/owned-process captures; showing a picture does not send it to the model. Use `attach:true` only for pixel analysis, which keeps the existing bounded analysis image by default.

Native application-window capture currently requires Windows. Covered windows can be captured when the application supports background rendering. Some GPU or minimized applications return blank or incomplete pixels; these produce an error, without switching windows or photographing the monitor. For background browser/VM work on supported platforms, use the explicit [WebDriver/QMP headless target](headless-control.md). No capture service, periodic readiness query or decoded-frame cache runs while these tools are unused.
