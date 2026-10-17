import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';
const runtime = readSource(sourcePath(testDir, '../../internal/webgui/shared-ui/runtime.js'), 'utf8');
function readSSE() {
  const context = {window: {}, TextDecoder};
  createSandbox(context); runScript(runtime, context);
  return context.window.SuperCliUI.readSSE;
}
function bodyFrom(chunks) {
  let index = 0;
  return {getReader() {return {async read() {
    return index < chunks.length ? {done: false, value: chunks[index++]} : {done: true};
  }};}};
}
async function collect(chunks) {
  const events = []; await readSSE()(bodyFrom(chunks), event => events.push(event));
  return JSON.parse(JSON.stringify(events));
}
test('SSE preserves ordering, multiline data and UTF-8 across every split delimiter', async () => {
  const text = ': heartbeat\r\n\r\ndata: {"type":"message","text":"ąć 日本語 🧪"}\n\n' +
    'data: {"type":\r\ndata: "reasoning", "text":"next"}\n\r\n' +
    'data: invalid JSON\r\n\n' +
    'data: {"type":"tool_result","text":"output"}\r\n\r\n' +
    'data: {"type":"done"}';
  const bytes = new TextEncoder().encode(text);
  const expected = [{type:'message',text:'ąć 日本語 🧪'}, {type:'reasoning',text:'next'},
    {type:'tool_result',text:'output'}, {type:'done'}];
  for (let split = 0; split <= bytes.length; split++)
    assert.deepEqual(await collect([bytes.subarray(0, split), bytes.subarray(split)]), expected, 'split ' + split);
  assert.deepEqual(await collect(Array.from(bytes, value => Uint8Array.of(value))), expected);
});
test('SSE parses a large fragmented tool result exactly once before following events', async () => {
  const output = 'long line '.repeat(60000) + '🧪 日本語';
  const expected = [{type:'tool_result',text:output}, {type:'message',text:'ready'}, {type:'done'}];
  const bytes = new TextEncoder().encode(expected.map(event => 'data: ' + JSON.stringify(event) + '\r\n\r\n').join(''));
  const chunks = []; for (let i = 0; i < bytes.length; i += 4096) chunks.push(bytes.subarray(i, i + 4096));
  assert.deepEqual(await collect(chunks), expected);
});
test('SSE emits a complete first frame before requesting another network chunk', async () => {
  let reads = 0; let firstEmitted = false;
  const body = {getReader() {return {async read() {
    reads++;
    if (reads === 1) return {done:false, value:new TextEncoder().encode('data: {"type":"message","text":"first"}\n\n')};
    assert.ok(firstEmitted, 'first frame waited for a later read');
    if (reads === 2) return {done:false, value:new TextEncoder().encode('data: {"type":"done"}\n\n')};
    return {done:true};
  }};}};
  await readSSE()(body, event => {if (event.type === 'message') {assert.equal(reads, 1); firstEmitted = true;}});
  assert.ok(firstEmitted);
});
test('SSE retains explicit errors, missing-terminal failures and upstream/callback errors', async () => {
  assert.deepEqual(await collect([new TextEncoder().encode('data: {"type":"error","err":"command failed"}\n\n')]), [{type:'error',err:'command failed'}]);
  await assert.rejects(collect([new TextEncoder().encode('data: {"type":"message","text":"unfinished"}\n\n')]), /before a terminal event/);
  assert.deepEqual(await collect([new TextEncoder().encode(': heartbeat\n\ndata: malformed\n\n')]), []);
  assert.deepEqual(await collect([]), []);
  const upstream = Error('connection failed');
  await assert.rejects(readSSE()({getReader() {return {async read() {throw upstream;}};}}, () => {}), error => error === upstream);
  const consumer = Error('consumer failed');
  await assert.rejects(readSSE()(bodyFrom([new TextEncoder().encode('data: {"type":"done"}\n\n')]), () => {throw consumer;}), error => error === consumer);
  await assert.rejects(readSSE()(null, () => {}), /body is unavailable/);
});
