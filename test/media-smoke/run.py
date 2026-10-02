import http.server,json,threading,subprocess,pathlib,signal,time,os,sys,tempfile,shutil,zlib,struct
from datetime import datetime,timezone
source=pathlib.Path(__file__).resolve().parents[2]
if len(sys.argv)!=2: raise SystemExit("usage: run.py /path/to/supercli-web")
binary=pathlib.Path(sys.argv[1]).resolve()
temporary=tempfile.TemporaryDirectory(prefix="supercli-media-smoke-")
root=pathlib.Path(temporary.name)
(root/'workspace').mkdir();(root/'data').mkdir()
def pngchunk(t,d): return struct.pack('!I',len(d))+t+d+struct.pack('!I',zlib.crc32(t+d)&0xffffffff)
w,h=320,180
pixels=b''.join(b'\0'+b''.join(bytes((x*255//w,y*255//h,140)) for x in range(w)) for y in range(h))
(root/'workspace/image.png').write_bytes(b'\x89PNG\r\n\x1a\n'+pngchunk(b'IHDR',struct.pack('!2I5B',w,h,8,2,0,0,0))+pngchunk(b'IDAT',zlib.compress(pixels))+pngchunk(b'IEND',b''))
shutil.copy(source/'internal/tools/mediagen/testdata/preview.mp4',root/'workspace/video.mp4')
(root/'mcp-fixture.cjs').write_text("""const fs=require('node:fs');const rl=require('node:readline').createInterface({input:process.stdin});
rl.on('line',line=>{let r=JSON.parse(line);if(!r.id)return;let result={};if(r.method==='initialize')result={protocolVersion:'2025-06-18',capabilities:{},serverInfo:{name:'fixture',version:'1'}};else if(r.method==='tools/list')result={tools:[{name:'screenshot',description:'Return local deterministic PNG fixture',inputSchema:{type:'object',properties:{}}}]};else if(r.method==='tools/call')result={content:[{type:'text',text:'native capture fixture'},{type:'image',mimeType:'image/png',data:fs.readFileSync(process.argv[2]).toString('base64')}]};process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:r.id,result})+'\\n');});""")
(root/'data/config.toml').write_text('default_provider = "fixture"\ndefault_model = "qwen-fixture"\nlanguage = "en"\n[[providers]]\nname = "fixture"\ntype = "openai"\nmodel = "qwen-fixture"\nbase_url = "http://127.0.0.1:18788/v1"\napi_key = "fixture-only"\n[mcp.servers.fixture]\ncommand = "node"\nargs = [{args}]\nconfirm_calls = true\nallowed_tools = ["screenshot"]\n'.format(args=", ".join(json.dumps(str(p)) for p in [root/'mcp-fixture.cjs',root/'workspace/image.png'])))
now=datetime.now(timezone.utc).isoformat()
(root/'data/pricing_cache.json').write_text(json.dumps({'fetched_at':now,'entries':[{'model_id':'qwen-fixture','context_length':16384,'input_per_1m':0,'output_per_1m':0,'source':'local-deterministic-fixture','fetched_at':now}]}))
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps({'data':[{'id':'qwen-fixture','object':'model'}]}).encode())
 def do_POST(self):
  req=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))));msgs=req.get('messages',[])
  outputs=[str(m.get('content','')) for m in msgs if m.get('role')=='tool']
  if not any('native capture fixture' in x for x in outputs):
   tool=('native-1','mcp_bridge',{'action':'call','server':'fixture','tool':'screenshot','arguments':{}})
  elif not any('video/mp4' in x for x in outputs):tool=('video-1','show_media',{'path':str(root/'workspace/video.mp4')})
  else:tool=None
  delta={'content':'Local fixture image and playable video are ready. No paid API was called.'}
  if tool:delta={'tool_calls':[{'index':0,'id':tool[0],'type':'function','function':{'name':tool[1],'arguments':json.dumps(tool[2])}}]}
  events=[{'id':'fixture','choices':[{'index':0,'delta':delta,'finish_reason':None}]},{'id':'fixture','choices':[{'index':0,'delta':{},'finish_reason':'tool_calls' if tool else 'stop'}],'usage':{'prompt_tokens':100,'completion_tokens':20,'total_tokens':120}}]
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  try:
   for ev in events:self.wfile.write(('data: '+json.dumps(ev)+'\n\n').encode());self.wfile.flush()
   self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()
  except BrokenPipeError:pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',18788),Handler);threading.Thread(target=server.serve_forever,daemon=True).start()
env={k:v for k,v in os.environ.items() if not k.startswith('SUPERCLI_')};env['GOMAXPROCS']='1';env['GOMEMLIMIT']='256MiB'
log=(root/'webgui.log').open('w')
p=subprocess.Popen([str(binary),'--home',str(root/'workspace'),'--data-dir',str(root/'data'),'--no-window','--addr','127.0.0.1:18789'],env=env,stdout=log,stderr=subprocess.STDOUT)
print('Local fixture GUI: http://127.0.0.1:18789',flush=True)
try:
 while p.poll() is None:time.sleep(1)
except KeyboardInterrupt:p.terminate();p.wait(timeout=10)
finally:server.shutdown();log.close();temporary.cleanup()
