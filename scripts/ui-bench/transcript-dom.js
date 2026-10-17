import {assert, runScript} from '../../test/ui/harness.js';
import fixture from '../transcript-dom-fixture.js';
import transcriptChecks from './transcript-performance-checks.js';

export default function transcriptDOMBench({baseline, reference}) {
  const current = fixture(''); current.baseline = baseline;
  // The historical parser's default was expanded reasoning. Match that
  // preference for canonical parity; explicit folded/history checks stay folded.
  current.ui.thinkingExpanded = true;
  const correctness = runScript('(' + transcriptChecks.toString() + ')({baseline})', current);
  const paragraphs = Array.from({length:400}, (_,i) => 'Paragraph '+i+' has **bold**, `code`, and ordinary streaming words.\n\n').join('');
  const code = '```js\n'+Array.from({length:1200}, (_,i) => 'const item'+i+' = "<&value>";\n').join('');
  function stream(overrides, text) {
    const c = fixture('', overrides); let chars = 0, calls = 0;
    const renderer = c.renderMarkdownish;
    c.renderMarkdownish = text => {chars += text.length; calls++; return renderer(text);};
    const node = c.addAssistantMsg(), start = performance.now();
    for (let i = 64; i < text.length+64; i += 64) {node._raw = text.slice(0,i); c.renderAssistant(node);}
    const result = {ms:performance.now()-start, markdownInputChars:chars, markdownCalls:calls, elements:node.querySelectorAll('*').length};
    // Detached completed fixture DOM must not remain rooted by the document.
    node.remove();
    assert.equal(c.document.querySelector('#stream').childNodes.length, 0);
    c.document.defaultView.frameElement.remove();
    return result;
  }
  function medianRuns(overrides, text) {
    stream(overrides,text);
    const runs = Array.from({length:5}, () => stream(overrides,text));
    return {...runs[0], ms:runs.map(r => r.ms).sort((a,b) => a-b)[2], runs:5};
  }
  const result = {baseline:reference, kind:'Native DOM fixture parsing/construction only; deterministic scheduling, not real FPS or WebView2 RAM', correctness, cases:{}};
  for (const [name,text] of Object.entries({paragraphs,code})) {
    const before = medianRuns(baseline,text), after = medianRuns({},text);
    assert.ok(after.markdownInputChars < before.markdownInputChars/10, 'Expected bounded-tail parsing for '+name);
    result.cases[name] = {sourceChars:text.length, chunkChars:64, before, after};
  }
  function history(overrides) {
    const c = fixture('',overrides), node = c.addAssistantMsg(); node._history = true;
    node._raw = '<thinking>'+paragraphs.repeat(5)+'</thinking>Answer';
    c.renderAssistant(node); node.querySelectorAll('details').forEach(d => d.open = false);
    const result = {sourceChars:node._raw.length, elements:node.querySelectorAll('*').length};
    c.document.defaultView.frameElement.remove();
    return result;
  }
  result.history = {before:history(baseline), after:history({})};
  assert.ok(result.history.after.elements < result.history.before.elements, 'Folded history must construct fewer elements than the baseline');
  current.document.defaultView.frameElement.remove();
  return result;
}
