'use strict';
function transcriptChecks({baseline}) {
      const referenceRender = new Function(baseline['03-markdown.js'] + ';return renderText;')();
      const normalized = node => {
        const clone = node.cloneNode(true);
        clone.normalize();
        function tree(node) {
          if (node.nodeType === 3) return node.data;
          return [node.nodeName, Array.from(node.attributes || []).map(a => [a.name, a.value]).sort(), Array.from(node.childNodes).filter(n => n.nodeType === 1 || (n.nodeType === 3 && n.data)).map(tree)];
        }
        return JSON.stringify(Array.from(clone.childNodes).filter(n => n.nodeType === 1 || (n.nodeType === 3 && n.data)).map(tree));
      };
      const expected = text => {
        const box = document.createElement('div'); box.innerHTML = referenceRender(text); return normalized(box);
      };
      function check(condition, message) {if (!condition) throw Error(message);}
      function paragraphPositions(node, text) {
        const box = document.createElement('div'); box.innerHTML = referenceRender(text);
        const positions = parent => Array.from(parent.querySelectorAll('p')).map(p => p === p.parentElement.lastElementChild);
        check(JSON.stringify(positions(node)) === JSON.stringify(positions(box)), 'Paragraph last-child semantics: ' + JSON.stringify(text));
      }
      const samples = [
        '<thinking>Thought</thinking>\n- first\n- second\n- third',
        '<thinking>Thought</thinking>\n| A | B |\n| --- | --- |\n| one | two |',
        '# Heading\n\nAlpha **bold** and [link](https://example.com).\n\n- first\n- second\n\n> quote\n> next\n\nTail',
        '| A | B |\n| --- | --- |\n| one | two |\n\nAfter',
        '| A | B |\n| :--- | ---: |\n| one **bold** | two |\n| third | fourth |\nAfter table',
        '- first **bold**\n+ second item\n* third item\n1. different list\n\nEnd',
        '1. first\n2) second\n3. third\n- switches type\nTail',
        'before\n\n```js\nconst a = "<tag>";\n\nconsole.log(a);\n```\n\nAfter',
        '```\n  spaces\n\n```\ntext```x```last',
        '<thinking>One\n\nTwo **bold**\n\n```txt\ncode\n```</thinking>\nAnswer\n\nMore',
        '<think>outer<think>inner</think>still</think>answer<think>later</think>',
        '</thinking>orphan\n\n<reflection>thought</reflection>tail',
        'before\n\n<!--\n\n-->\n\nAfter',
        'a\r<!---->\n\nb',
        'a\u2028<!---->\n\nb',
        'a\u2029  <!---->\n\nb',
        'a\n<!---->\n\n\rb',
        'a\n<!---->\n\n\u2028b',
        'a\n<!---->\n\n\u2029b',
        'emoji 🧪 polskie ąćę 日本語\n\n<script>alert(1)</script> & entity',
      ];
      let prefixes = 0;
      for (const sample of samples) {
        const node = addAssistantMsg();
        for (let i = 1; i <= sample.length; i++) {
          node._raw = sample.slice(0, i); renderAssistant(node);
          check(normalized(node) === expected(node._raw), 'Prefix parity at ' + i + ': ' + JSON.stringify(node._raw) + '\n' + normalized(node) + '\n' + expected(node._raw));
          check(renderText(node._raw) === referenceRender(node._raw), 'Reference parser parity');
          paragraphPositions(node, node._raw);
          prefixes++;
        }
        // Recovery may replace the whole snapshot, including with a shorter one.
        node._raw = 'Replacement **snapshot**'; renderAssistant(node);
        check(normalized(node) === expected(node._raw), 'Recovery snapshot parity');
        node.remove();
      }
      // Check native HTML parsing and keep completed rows across marker transitions.
      for (const source of ['| A | B |\n| --- | --- |\n| first | row |\n', '- first item\n- second item\n']) {
        const growing = addAssistantMsg(); growing._raw = source; renderAssistant(growing);
        const row = growing.querySelector(source[0] === '|' ? 'tbody tr' : 'li');
        for (const chunk of (source[0] === '|' ? '| next | row |\n' : '- next item\n')) {
          growing._raw += chunk; renderAssistant(growing);
          check(growing.querySelector(source[0] === '|' ? 'tbody tr' : 'li') === row, 'Completed row identity retained');
          check(normalized(growing) === expected(growing._raw), 'Growing row prefix parity');
          prefixes++;
        }
        growing.remove();
      }

      const paragraph = addAssistantMsg();
      paragraph._raw = 'Stable prose'; renderAssistant(paragraph);
      const p = paragraph.querySelector('p'), textNode = p.firstChild;
      for (const piece of [' & <escaped> ', 'emoji 😀 ', '**bold** ', 'plus [link](https://example.com)']) {
        paragraph._raw += piece; renderAssistant(paragraph);
        check(paragraph.querySelector('p') === p, 'Growing paragraph identity');
        check(normalized(paragraph) === expected(paragraph._raw), 'Growing paragraph formatting');
        if (piece.indexOf('**') < 0 && piece.indexOf('[link]') < 0) check(p.firstChild === textNode, 'Plain text node identity');
        prefixes++;
      }
      paragraph.remove();
      const folded = addAssistantMsg();
      folded._history = true;
      folded._raw = '<thinking>' + ('Paragraph **bold**.\n\n'.repeat(2000)) + '</thinking>Final answer';
      renderAssistant(folded);
      const before = folded.querySelectorAll('*').length;
      const detail = folded.querySelector('details');
      check(!detail.open && !detail.querySelector('.think-content').children.length, 'History thought must be lazy');
      detail.open = true; detail.dispatchEvent(new Event('toggle'));
      check(normalized(folded) === expected(folded._raw), 'Opening history restores all reasoning');
      paragraphPositions(folded, folded._raw);
      const after = folded.querySelectorAll('*').length;
      detail.open = false; detail.dispatchEvent(new Event('toggle'));
      detail.open = true; detail.dispatchEvent(new Event('toggle'));
      check(folded.querySelectorAll('*').length === after, 'No duplicated reasoning on reopen');
      folded.remove();
      const live = addAssistantMsg();
      live._raw = '<think>first</think>Answer\n\n'; renderAssistant(live);
      const thinking = live.querySelector('details'); thinking.open = false;
      live._raw += 'Next'; renderAssistant(live);
      check(live.querySelector('details') === thinking && !thinking.open, 'Live folded state retained');
      live.remove();
      // A tool boundary flushes all pending text synchronously before its row.
      let current = handleEvent({type:'message', text:'one'}, null);
      current = handleEvent({type:'message', text:' two'}, current);
      const assistant = current;
      current = handleEvent({type:'tool_call', id:'perf-tool', name:'read_lines', args:'{"path":"fake.txt"}'}, current);
      check(current === null && assistant.textContent === 'one two' && assistant._renderTimer === null, 'Tool boundary flush');
      handleEvent({type:'tool_result', id:'perf-tool', output:'1 | synthetic', err:''}, current);
      current = handleEvent({type:'message', text:'after tool'}, current);
      sealAssistantSegment(current);
      check(current.textContent === 'after tool' && assistant.textContent === 'one two', 'Tool order and sealed segments');
      check(normalized(current) === expected(current._raw), 'Final flush preserves original element hierarchy');
      paragraphPositions(current, current._raw);
      stream.textContent = ''; toolRows = {}; openToolOrder = [];
      return {prefixes, foldedReasoningElements: before, expandedReasoningElements: after};
}
module.exports = transcriptChecks;
