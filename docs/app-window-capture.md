# Application window screenshots

Use the target application rather than the currently visible monitor when demonstrating a program built or tested by the agent. Windows capture never activates a window or takes focus. Programs started by `process_session` open minimized without focus (`visible:true` shows them normally). A minimized window is captured by briefly restoring it fully transparent, click-through and at the bottom of the Z-order, then minimizing it again. There is no fallback to the foreground screen.

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

Native application-window capture currently requires Windows. Covered windows can be captured when the application supports background rendering. Some GPU applications still return blank or incomplete pixels; these produce an error, without switching windows or photographing the monitor. For background browser/VM work on supported platforms, use the explicit [WebDriver/QMP headless target](headless-control.md). No capture service, periodic readiness query or decoded-frame cache runs while these tools are unused.

## Background window accessibility

On Windows, the same managed session supports reading controls and invoking their accessibility actions:

```json
{"action":"inspect_window","id":"proc-1"}
```

The result includes the selected `window_id` and up to 128 elements (at most 12 levels deep), with names, roles, automation IDs, bounded field values and available actions. Password values and their subtrees are omitted. Large field values are marked `value_truncated`; overwriting partially observed values is refused.

Use an element ID from that observation:

```json
{"action":"type","id":"proc-1","element_id":"el_...","text":"New field value"}
{"action":"click","id":"proc-1","element_id":"el_..."}
```

`type` replaces the complete field value through ValuePattern. `click` uses Invoke, Toggle or SelectionItem, depending on the control. These are [Windows UI Automation control patterns](https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-controlpatternsoverview), not simulated desktop input. SuperCli does not move the mouse, inject keyboard events, focus the target, or use the clipboard. Unsupported controls fail without a physical-input fallback. The application itself can still open dialogs or perform its normal action when invoked.

An element ID includes its process creation identity, window, runtime ID and observed semantics/value. Changed or replaced controls require another observation. Target ownership is checked before and after the operation; a shell launcher does not grant control over arbitrary descendants or other applications.

For an external program, these `process_session` operations (including `screenshot`) accept `process_id` instead of a managed `id`. No confirmation dialog is shown: the operations never take the user's mouse or keyboard. The external process is pinned through the operation, so a reused PID is never targeted. It is not adopted as a managed process and is never terminated by `stop` or SuperCli shutdown.

The `dispatched` result means the accessibility provider accepted the action, not that the application's resulting work has finished or succeeded. If an action is interrupted after dispatch, its effects may already exist; inspect the state rather than blindly repeating it. A hidden helper bounds native provider calls to 15 seconds without progress polling. No accessibility helper or tree cache runs while the tools are idle.

This requires the target to expose suitable accessibility controls. Custom-rendered games/editors, another process's embedded controls, protected/elevated applications and incomplete accessibility providers may be unsupported. The feature does not bypass those limits or automatically escalate to physical mouse control.
