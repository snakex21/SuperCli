"""Measure the full process tree of two Windows GUI builds on fixed phase boundaries.

Requires Python and psutil. Profiles, copied UI and measurements stay beneath the
repository's .tmp folder by default. Completion notifications replace progress
polling; no production data or model endpoints are used.
"""

import argparse
import ctypes as ct
from ctypes import wintypes as wt
import json
import os
from pathlib import Path
import queue
import shutil
import subprocess
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import psutil

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--baseline', type=Path, required=True)
parser.add_argument('--candidate', type=Path, required=True)
parser.add_argument('--output-dir', type=Path, default=root / '.tmp/webview-memory-measurement')
parser.add_argument('--minimized-seconds', type=int, default=30)
parser.add_argument('--rounds', type=int, default=2)
args = parser.parse_args()
if sys.platform != 'win32':
    parser.error('This benchmark requires Windows and WebView2.')
if not 1 <= args.rounds <= 5 or not 1 <= args.minimized_seconds <= 300:
    parser.error('Use 1..5 rounds and 1..300 seconds per minimized phase.')
stage = args.output_dir.resolve()
stage.mkdir(parents=True, exist_ok=True)
minimized_seconds = args.minimized_seconds
for executable in [args.baseline, args.candidate]:
    if not executable.resolve().is_file():
        parser.error('Missing executable: ' + str(executable))
user32 = ct.WinDLL('user32', use_last_error=True)
user32.GetWindowThreadProcessId.argtypes = [wt.HWND, ct.POINTER(wt.DWORD)]
user32.IsIconic.argtypes = [wt.HWND]
user32.IsWindowVisible.argtypes = [wt.HWND]
user32.ShowWindowAsync.argtypes = [wt.HWND, ct.c_int]
user32.ShowWindowAsync.restype = wt.BOOL
user32.SendMessageTimeoutW.argtypes = [wt.HWND, wt.UINT, wt.WPARAM, wt.LPARAM, wt.UINT, wt.UINT, ct.POINTER(ct.c_size_t)]
user32.SendMessageTimeoutW.restype = ct.c_size_t
enum_callback = ct.WINFUNCTYPE(wt.BOOL, wt.HWND, wt.LPARAM)
user32.EnumWindows.argtypes = [enum_callback, wt.LPARAM]

def find_window(pid):
    found = []
    @enum_callback
    def visit(hwnd, _):
        owner = wt.DWORD()
        user32.GetWindowThreadProcessId(hwnd, ct.byref(owner))
        if owner.value == pid and user32.IsWindowVisible(hwnd):
            found.append(hwnd)
        return True
    user32.EnumWindows(visit, 0)
    if len(found) != 1:
        raise RuntimeError('Expected one visible window, got ' + str(len(found)))
    return found[0]

def command(hwnd, value):
    result = ct.c_size_t()
    if not user32.SendMessageTimeoutW(hwnd, 0x0112, value, 0, 0x0002, 10000, ct.byref(result)):
        raise ct.WinError(ct.get_last_error())

def memory(pid):
    parent = psutil.Process(pid)
    processes = [parent] + parent.children(recursive=True)
    rows = []
    for process in processes:
        try:
            info = process.memory_full_info()
            role = 'host' if process.pid == pid else 'browser'
            for argument in process.cmdline():
                if argument.startswith('--type='):
                    role = argument.split('=', 1)[1]
            rows.append(dict(pid=process.pid, role=role, private_bytes=info.private, private_working_set=info.uss, working_set=info.rss))
        except (psutil.NoSuchProcess, psutil.AccessDenied) as error:
            raise RuntimeError('Incomplete process census: ' + str(error))
    return dict(processes=rows, process_count=len(rows), private_bytes=sum(row['private_bytes'] for row in rows), private_working_set=sum(row['private_working_set'] for row in rows), working_set_sum=sum(row['working_set'] for row in rows), system_available_bytes=psutil.virtual_memory().available)

def one_case(executable, label, flags, index):
    run = stage / 'runs' / (label + '-' + str(index) + '-' + uuid.uuid4().hex[:8])
    run.mkdir(parents=True)
    ui = run / 'ui'
    shutil.copytree(root / 'internal/webgui/assets', ui)
    project = run / 'project'
    project.mkdir()
    notifications = queue.Queue()
    instructions = queue.Queue()
    nonce = uuid.uuid4().hex
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_POST(self):
            if self.path.startswith('/' + nonce + '/'):
                notifications.put(('page', self.path.rsplit('/', 1)[1]))
                self.send_response(204)
                self.send_header('Access-Control-Allow-Origin', '*')
                self.end_headers()
            else:
                self.send_error(404)
        def do_GET(self):
            if self.path != '/' + nonce + '/events':
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Access-Control-Allow-Origin', '*')
            self.end_headers()
            while True:
                event = instructions.get()
                if event is None:
                    return
                try:
                    self.wfile.write(('data: ' + event + '\n\n').encode())
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    return
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server.daemon_threads = True
    server_thread = threading.Thread(target=server.serve_forever, kwargs=dict(poll_interval=1), daemon=True)
    # HTTP serve_forever's internal shutdown loop is not a process/progress query.
    server_thread.start()
    endpoint = 'http://127.0.0.1:' + str(server.server_port) + '/' + nonce
    init_path = ui / 'js/12-keyboard-init.js'
    init_source = init_path.read_text(encoding='utf-8')
    marker = '  promptEl.focus();\n})();'
    if init_source.count(marker) != 1:
        raise RuntimeError('GUI initialization marker changed')
    init_path.write_text(init_source.replace(marker, '''  promptEl.focus();
  window.__supercliProbeAppReady=true;
  window.dispatchEvent(new Event('supercli-probe-app-ready'));
})();'''), encoding='utf-8')
    script = '''<script>
    window.addEventListener('load',()=>{
      const s=new EventSource(ENDPOINT+'/events');
      let streamReady=false, notified=false;
      const ready=()=>{
        if(streamReady&&window.__supercliProbeAppReady&&!notified){
          notified=true;
          fetch(ENDPOINT+'/ready',{method:'POST',mode:'no-cors'});
        }
      };
      window.addEventListener('supercli-probe-app-ready',ready);
      s.onopen=()=>{streamReady=true;ready();};
      s.onmessage=async e=>{
        if(e.data==='restored') await new Promise(requestAnimationFrame);
        fetch(ENDPOINT+'/'+e.data,{method:'POST',mode:'no-cors'});
      };
    });
    </script>'''.replace('ENDPOINT', json.dumps(endpoint))
    html = ui / 'index.html'
    html.write_text(html.read_text(encoding='utf-8').replace('</body>', script + '</body>'), encoding='utf-8')
    environment = os.environ.copy()
    # Affect only this isolated experimental child's environment.
    if flags:
        environment['WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS'] = flags
    else:
        environment.pop('WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS', None)
    log_file = (run / 'launcher.log').open('wb')
    arguments = [str(executable), '--echo', '--data-dir', str(run / 'data'), '--home', str(project), '--ui-dir', str(ui), '--app-name', 'SuperCli RAM probe', '--app-profile', 'ram-' + nonce]
    started = time.monotonic()
    process = subprocess.Popen(arguments, cwd=root, env=environment, stdout=log_file, stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW)
    threading.Thread(target=lambda: notifications.put(('exit', process.wait())), daemon=True).start()
    samples = []
    try:
        event = notifications.get(timeout=120)
        if event != ('page', 'ready'):
            raise RuntimeError('Startup failed: ' + str(event))
        hwnd = find_window(process.pid)
        startup = time.monotonic() - started
        for phase, action, seconds, iconic in [('visible', None, 6, False), ('minimized', 7, minimized_seconds, True), ('restored', 9, 6, False)]:
            if action is not None:
                if not user32.ShowWindowAsync(hwnd, action):
                    raise ct.WinError(ct.get_last_error())
            # One predeclared settling interval, then one measurement; no progress polling.
            threading.Event().wait(seconds)
            if bool(user32.IsIconic(hwnd)) != iconic:
                raise RuntimeError('Window transition failed: ' + phase)
            ack_started = time.monotonic()
            instructions.put(phase)
            event = notifications.get(timeout=10)
            if event != ('page', phase):
                raise RuntimeError('SSE/script continuity failed: ' + str(event))
            samples.append(dict(phase=phase, page_sse_ack=True, page_sse_ack_ms=1000*(time.monotonic()-ack_started), **memory(process.pid)))
        command(hwnd, 0xF060)
        code = process.wait(timeout=20)
        if code != 0:
            raise RuntimeError('GUI exit failed: ' + str(code))
        receipt = dict(label=label, index=index, executable=str(executable), flags=flags, minimized_seconds=minimized_seconds, ready_seconds=startup, phases=samples, exit_code=code, run=str(run))
        (run / 'measurement.json').write_text(json.dumps(receipt, indent=2), encoding='utf-8')
        return receipt
    finally:
        if process.returncode is None:
            psutil.Process(process.pid).terminate()
            process.wait(timeout=15)
        instructions.put(None)
        server.shutdown()
        server.server_close()
        log_file.close()

if __name__ == '__main__':
    cases = [(args.baseline.resolve(), 'baseline', ''), (args.candidate.resolve(), 'memory-api', '')]
    results = []
    for index in range(1, args.rounds+1):
        order = cases if index % 2 else list(reversed(cases))
        for executable, label, flags in order:
            result = one_case(executable, label, flags, index)
            results.append(result)
            (stage / 'measurements.json').write_text(json.dumps(results, indent=2), encoding='utf-8')
    print(json.dumps([dict(label=r['label'], index=r['index'], ready_seconds=r['ready_seconds'], phases=[{k:v for k,v in p.items() if k!='processes'} for p in r['phases']]) for r in results]))
