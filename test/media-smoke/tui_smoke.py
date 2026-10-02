import sys,os,pty,subprocess,tempfile,pathlib,json,time,select,termios,fcntl,struct,signal
from datetime import datetime,timezone
if len(sys.argv)!=2:raise SystemExit('usage: tui_smoke.py /path/to/supercli')
binary=pathlib.Path(sys.argv[1]).resolve()
with tempfile.TemporaryDirectory(prefix='supercli-tui-offline-') as d:
 root=pathlib.Path(d);data=root/'data';data.mkdir();project=root/'project';project.mkdir()
 now=datetime.now(timezone.utc).isoformat()
 (data/'config.toml').write_text('language = "en"\nno_color = true\n')
 (data/'pricing_cache.json').write_text(json.dumps({'fetched_at':now,'entries':[{'model_id':'echo','context_length':16384,'input_per_1m':0,'output_per_1m':0,'source':'local-deterministic-fixture','fetched_at':now}]}))
 master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,80,0,0))
 original=termios.tcgetattr(slave)
 env={k:v for k,v in os.environ.items() if not k.startswith('SUPERCLI_')};env.update(TERM='xterm-256color',NO_COLOR='1',GOMAXPROCS='1')
 proc=subprocess.Popen([str(binary),'--echo','--home',str(project),'--data-dir',str(data)],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
 output=bytearray()
 def read(seconds):
  until=time.monotonic()+seconds
  while time.monotonic()<until:
   if proc.poll() is not None:break
   ready,_,_=select.select([master],[],[],.1)
   if ready:
    try:b=os.read(master,65536)
    except OSError:break
    output.extend(b)
    if b'\x1b[6n' in b:os.write(master,b'\x1b[1;1R')
 try:
  read(1.5);os.write(master,b'/help\r');read(.8);os.write(master,b'\x1b');read(.2);os.write(master,b'\x15');read(.2);quit_at=time.monotonic();os.write(master,b'/quit\r');read(1);quit_elapsed=time.monotonic()-quit_at
  try:proc.wait(timeout=5)
  except subprocess.TimeoutExpired:
   print(output.decode(errors='replace'));raise
  text=output.decode(errors='replace')
  if proc.returncode!=0 or 'SuperCli' not in text or '/help' not in text:raise RuntimeError(repr(text[-2000:]))
  if termios.tcgetattr(slave)!=original:raise RuntimeError('terminal modes not restored')
  print(json.dumps({'result':'PASS','surface':'Linux actual 80x24 PTY','observed':['startup SuperCli header','/help rendering','/quit exit 0','terminal modes restored'],'offline_pricing_cache':True,'output_bytes':len(output),'quit_exit_observed_seconds':round(quit_elapsed,3)}))
 finally:
  if proc.poll() is None:os.killpg(proc.pid,signal.SIGTERM);proc.wait(timeout=5)
  os.close(slave);os.close(master)
