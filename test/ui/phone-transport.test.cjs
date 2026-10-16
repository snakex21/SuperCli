const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/shared-ui/runtime.js'), 'utf8');

function harness(hostname) {
  const sockets = [], fetches = [];
  class Socket {
    constructor(url) { this.url = url; this.sent = []; sockets.push(this); }
    send(value) { this.sent.push(value); }
    close() { this.closed = true; }
  }
  const context = {window:{location:{hostname, host:hostname, protocol:'https:'}}, WebSocket:Socket, ReadableStream, TextDecoder, TextEncoder, DOMException,
    fetch:async (...args) => { fetches.push(args); return {ok:true,body:'local SSE'}; }};
  vm.createContext(context); vm.runInContext(source, context);
  return {ui:context.window.SuperCliUI, sockets, fetches};
}

test('phone transport preserves local SSE without opening WebSocket', async () => {
  for (const host of ['localhost', '127.0.0.1', '127.5.6.7', '[::1]']) {
    const h = harness(host), options = {method:'POST',body:'{}'};
    const response = await h.ui.chatResponse('/api/chat', options);
    assert.equal(response.body, 'local SSE'); assert.equal(h.fetches[0][1], options); assert.equal(h.sockets.length, 0);
  }
});
test('phone WebSocket exposes each live frame to the same SSE parser before close', async () => {
  const h = harness('test.trycloudflare.com'), seen = [], ctl = new AbortController();
  const pending = h.ui.chatResponse('/api/chat', {body:'{"prompt":"hello"}',signal:ctl.signal});
  const ws = h.sockets[0]; assert.equal(ws.url, 'wss://test.trycloudflare.com/api/chat/ws');
  ws.onopen(); const response = await pending;
  const stream = h.ui.readSSE(response.body, event => seen.push(event));
  ws.onmessage({data:'data: {"type":"token","text":"live"}\n\n'});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(seen.length, 1); assert.equal(seen[0].text, 'live');
  assert.deepEqual(ws.sent, ['{"prompt":"hello"}']); assert.equal(h.fetches.length, 0);
  ws.onmessage({data:'data: {"type":"done"}\n\n'}); ws.onclose({code:1000});
  await stream; assert.equal(seen[seen.length - 1].type, 'done');
});
test('phone abort closes the owned WebSocket and does not retry over HTTP', async () => {
  const h = harness('test.trycloudflare.com'), ctl = new AbortController();
  const pending = h.ui.chatResponse('/api/chat', {body:'{}',signal:ctl.signal});
  const ws = h.sockets[0]; ws.onopen(); const response = await pending;
  const stream = h.ui.readSSE(response.body, () => {}); ctl.abort();
  await assert.rejects(stream, {name:'AbortError'}); assert.equal(ws.closed, true); assert.equal(h.fetches.length, 0);
});
test('phone abnormal disconnect remains an error, without a hidden transport fallback', async () => {
  const h = harness('test.trycloudflare.com');
  const pending = h.ui.chatResponse('/api/chat', {body:'{}'}); const ws = h.sockets[0];
  ws.onopen(); const response = await pending; const stream = h.ui.readSSE(response.body, () => {});
  ws.onclose({code:1006}); await assert.rejects(stream, /disconnected/); assert.equal(h.fetches.length, 0);
});
