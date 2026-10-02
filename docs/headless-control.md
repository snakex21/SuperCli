# Local headless control

SuperCli can control an explicitly selected local QEMU display through QMP, and Chrome/Firefox through an existing WebDriver server. It does not scan for applications, open a browser, or keep a control connection while the tool is unused. The adapters use Go's standard library; no Node, Python, Selenium library or browser runtime is bundled.

QEMU and a browser driver are optional external programs. Launch them once with the existing process_session tool, then reuse the endpoint/session_id. Its default lifetime remains ten minutes; timeout_ms can explicitly extend a managed process to 24 hours. Closing SuperCli closes its managed processes. Connecting headless_control to an externally started VM/driver never transfers ownership of that process.

## Trusted target scope and consent

Enable only the intended automation endpoints in the portable global `supercli-data/config.toml`:

```toml
[headless.targets.my_vm]
protocol = "qmp"
endpoint = "tcp://127.0.0.1:4444"
allowed_actions = ["status", "screenshot", "wait_event", "keys", "click"]

[headless.targets.my_browser]
protocol = "webdriver"
endpoint = "http://127.0.0.1:9515"
allowed_actions = ["open", "status", "inspect", "navigate", "click", "type", "screenshot", "close"]
```

There is no default target or wildcard. Empty targets/actions deny access. Project configuration and model arguments cannot add targets. The tool snapshots the global configuration at construction; restart/recreate the session after an operator config change. Matching uses the exact normalized protocol, loopback endpoint and WebDriver base path. Read-only status/inspection/capture also require an allowed target and action.

Mutations additionally require interactive **Allow once** approval before any endpoint connection or profile creation. The confirmation shows the configured target, endpoint, session ID and complete arguments, including URL, selector, text, keys, browser/binary and coordinates. It describes browser launch/profile creation or session closure. Missing UI, Cancel, interruption and arbitrary answer text deny the operation. TUI starts at Cancel; scroll all details, then use Tab to select Allow once. A small terminal cannot approve. A headless/batch invocation can use permitted read-only actions, but cannot silently approve mutations.

The allowlist trusts the operator's chosen local endpoint; it does not authenticate which process binds that port. Start and inspect the intended driver/VM yourself, and do not reuse a configured port for an unrelated application. This protection covers the native headless tool; launching arbitrary processes remains a separate capability.

## QEMU

Launch your explicitly selected VM with -display none and a loopback QMP server, for example these additional QEMU arguments:

    -display none -qmp tcp:127.0.0.1:4444,server=on,wait=off

The VM's own disk, ISO, memory and machine arguments remain the user's/project's choice. SuperCli does not guess a disk, reinstall an existing VM, or modify the host's boot configuration.

    {"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"status"}
    {"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"keys","keys":["ctrl","alt","delete"]}
    {"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"click","x":16384,"y":16384}
    {"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"screenshot"}
    {"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":"wait_event","event":"SHUTDOWN","timeout_ms":300000}

Coordinates are QMP absolute coordinates 0–32767, not host-screen pixels. A click and a complete key chord are each one ordered protocol command. QMP wait_event blocks on incoming events; it does not issue repeated query-status requests. It observes new events on that connection, not historical events from a prior disconnected call. Screenshots require QEMU 7.1+ with PNG support and access to SuperCli's local capture directory; remote/container QMP endpoints are not supported.

A control operation has a bounded deadline (30 seconds by default, up to five minutes). Cancellation closes the control connection, not the attached VM. Concurrent operations to the same endpoint are serialized so different workers cannot interleave a chord; different endpoints remain independent.

## Headless browsers

Start an installed ChromeDriver or GeckoDriver with process_session on an explicit loopback port. Give that managed process portable TMP/TEMP/TMPDIR values in env if the driver needs temporary files, all under the application/project folder.

    {"protocol":"webdriver","endpoint":"http://127.0.0.1:9515","action":"open","browser":"chrome","url":"http://127.0.0.1:8080"}
    {"protocol":"webdriver","endpoint":"http://127.0.0.1:9515","session_id":"RETURNED_ID","action":"inspect"}
    {"protocol":"webdriver","endpoint":"http://127.0.0.1:9515","session_id":"RETURNED_ID","action":"click","selector":"#save"}
    {"protocol":"webdriver","endpoint":"http://127.0.0.1:9515","session_id":"RETURNED_ID","action":"type","selector":"#name","text":"Example"}
    {"protocol":"webdriver","endpoint":"http://127.0.0.1:9515","session_id":"RETURNED_ID","action":"screenshot"}
    {"protocol":"webdriver","endpoint":"http://127.0.0.1:9515","session_id":"RETURNED_ID","action":"close"}

open creates an explicitly headless Chrome/Firefox session with its profile under DataDir/.supercli/headless. binary can select a portable browser executable. Explicit close deletes that WebDriver session and only SuperCli's matching profile record/directory; it does not stop the driver or unrelated sessions. Existing session IDs can be attached without claiming their profiles. No endpoint proxy, redirects, host mouse or foreground activation is used.

inspect returns bounded visible text and uniquely selected controls instead of pixels. Prefer this for web forms and navigation to avoid repeated screenshot inference. Password values are omitted. Inspection is bounded (2,000 visited DOM nodes, 60 controls); truncated results say so. The caller can navigate, click, or type directly with CSS selectors. Browser page navigation uses the driver's completion response rather than a client-side polling loop. Browser driver compatibility and the application's own resource consumption remain external to SuperCli.

## Images and resource boundaries

A screenshot is saved original and automatically appears in live/restored GUI chat. It does not become model image input unless attach:true is supplied. Attached analysis uses the same 1280-long-edge/one-megapixel default budget as read_image; image_detail:original opts out, and read_image supports precise original-coordinate crops. GUI thumbnails use the existing bounded lazy preview path. TUI receives the same tool result/path and can expand it using its normal tool-detail controls.

Each operation releases its network buffers/transport when complete. There is no always-running capture worker, event poller or retained decoded-frame cache. Endpoint gates are capped and removed after the last operation. Ordinary turns carry no headless/process-launcher schemas merely because this feature is installed. Explicit headless/QMP/WebDriver requests receive those contracts immediately, avoiding a discovery inference.

## Verification

Tests use local protocol fixtures and synthetic images, not the user's desktop or VM disks. They check ordered QMP input, asynchronous events, cancellation/disconnection, response/argument limits, portable snapshots, image attachment defaults, WebDriver requests/profile ownership, unique DOM selectors, and request-scoped schemas. They do not establish successful unattended installation of a particular guest OS, or compatibility with every external browser/driver version.

Protocol references: [QMP](https://www.qemu.org/docs/master/interop/qemu-qmp-ref.html), [QMP wire format](https://www.qemu.org/docs/master/interop/qmp-spec.html), [WebDriver](https://www.w3.org/TR/webdriver2/), [Chrome options](https://developer.chrome.com/docs/chromedriver/capabilities).
