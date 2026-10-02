import urllib.request,urllib.parse,json,threading,time,pathlib,subprocess,os,signal,sys
root=pathlib.Path(__file__).resolve().parent
if len(sys.argv)!=2: raise SystemExit('usage: http_smoke.py /path/to/supercli-web')
runner=subprocess.Popen([sys.executable,str(root/'run.py'),str(pathlib.Path(sys.argv[1]).resolve())],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,start_new_session=True)
base='http://127.0.0.1:18789'
def get(path):
 return urllib.request.urlopen(base+path,timeout=10)
def post(path,data):
 return urllib.request.urlopen(urllib.request.Request(base+path,data=json.dumps(data).encode(),headers={'Content-Type':'application/json','Origin':base}),timeout=20)
try:
 for _ in range(50):
  try:
   health=json.load(get('/api/health'));break
  except Exception:time.sleep(.1)
 else:raise RuntimeError('server unavailable')
 events=[];images=[];videos=[];session='';questions=0
 response=post('/api/chat',{'prompt':'Show the local fixture image and video','turn_id':'local-fixture-smoke'})
 for line in response:
  if not line.startswith(b'data: '):continue
  event=json.loads(line[6:]);events.append(event)
  if event['type']=='session':session=event['session_id']
  if event['type']=='question':
   q=event['question'];assert 'fixture' in q['question'] and 'screenshot' in q['question'];assert not q['allow_custom'];questions+=1
   with post('/api/question/answer',{'id':q['id'],'selected':['Allow once']}):pass
  if event['type']=='tool_result':
   for ref in event.get('images',[]):
    raw=get('/api/attachment/preview?path='+urllib.parse.quote(ref)).read();assert raw.startswith(b'\x89PNG');images.append(len(raw));thumb=get('/api/attachment/preview?path='+urllib.parse.quote(ref)+'&thumbnail=transcript').read();assert thumb.startswith(b'\x89PNG')
   try:meta=json.loads(event.get('output',''))
   except Exception:continue
   if meta.get('type')=='video':
    req=urllib.request.Request(base+'/api/attachment/preview?path='+urllib.parse.quote(meta['path']),headers={'Range':'bytes=0-23'})
    with urllib.request.urlopen(req) as result:
     raw=result.read();assert result.status==206 and b'ftyp' in raw;videos.append(len(raw))
 assert images and videos and questions==1,(images,videos,questions,[x['type'] for x in events])
 receipt=json.load(get('/api/chat/completion?id=local-fixture-smoke'));assert receipt['session_id']==session and receipt['accepted']
 transcript=json.load(get('/api/transcript?id='+urllib.parse.quote(session)))
 messages=transcript.get('messages',[]) if isinstance(transcript,dict) else transcript
 assert any(any(p.startswith('session:') for p in m.get('attachments',[])) for m in messages)
 out={'result':'PASS','scope':'actual Linux GUI HTTP/SSE backend and child MCP, no browser renderer or paid provider','events':[e['type'] for e in events],'image_bytes':images,'video_range_bytes':videos,'confirmation_questions':questions,'durable_session':bool(session),'replay_has_portable_image':True}
 (pathlib.Path.cwd()/'docs/evals/supercli-media-perf-20261002/http-smoke.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps(out))
finally:
 os.killpg(runner.pid,signal.SIGINT)
 try:runner.wait(timeout=15)
 except subprocess.TimeoutExpired:os.killpg(runner.pid,signal.SIGKILL);runner.wait()
