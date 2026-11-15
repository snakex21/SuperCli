import {assert, test, readSource, sourcePath, testDir, parseHTML} from './harness.js';

// The right side panel's tab row (Stats / Sessions / Projects / Tasks / Goal)
// is uppercase with letter-spacing. With long words ("STATYSTYKI ROZMOWY",
// "STATISTIKEN GESPRÄCHE") and the narrowest panel, labels used to run into
// each other. Every locale is rendered at the narrowest supported panel width
// and at 400 px, with the app font stack and with a wide serif fallback (the
// font the bug report showed): no two labels may intersect, no label may be
// clipped inside its tab, and the row may not overflow the panel (it scrolls
// inside it instead).
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const css = readSource(sourcePath(assets, 'app.css'), 'utf8');
const languages = ['en','bg','cs','da','de','el','es','et','fi','fr','hr','hu','it','lt','lv','nb','nl','pl','pt-BR','ro','ru','sk','sl','sr-Latn','sv','tr','uk'];
const keys = ['stats', 'sessions', 'projects', 'tasks', 'goal'];
const narrowest = Number(/\.sidebar\s*\{[^}]*width:\s*clamp\((\d+)px/.exec(css)?.[1]);

function render(lang, width, font) {
  const catalog = JSON.parse(readSource(sourcePath(assets, 'locales', lang + '.json'), 'utf8'));
  const buttons = keys.map((key, i) => `<button data-tab="${key}"${i === 1 ? ' class="active"' : ''}>${catalog['side.' + key]}</button>`).join('');
  const {document} = parseHTML(`<!doctype html><html><head><style>${css}</style>
    <style>${font ? `body,button{font-family:${font} !important}` : ''} .sidebar{width:${width}px !important}</style></head>
    <body><div class="shell" style="display:flex"><aside class="sidebar"><div class="sidebar-head">
    <nav class="side-tabs" id="side-tabs">${buttons}</nav></div></aside></div></body></html>`);
  return document;
}

function intersects(a, b) {
  return a.left < b.right - 0.5 && b.left < a.right - 0.5 && a.top < b.bottom - 0.5 && b.top < a.bottom - 0.5;
}

test('side panel tab labels never overlap or overflow in any locale', () => {
  assert.ok(narrowest >= 200 && narrowest <= 400, 'narrowest sidebar width parsed from app.css: ' + narrowest);
  for (const font of ['', 'Georgia, "Times New Roman", serif']) {
    for (const width of [narrowest, 400]) {
      for (const lang of languages) {
        const document = render(lang, width, font);
        const where = `${lang} @${width}px${font ? ' serif' : ''}`;
        const sidebar = document.querySelector('.sidebar').getBoundingClientRect();
        const nav = document.querySelector('#side-tabs');
        const navRect = nav.getBoundingClientRect();
        assert.ok(navRect.left >= sidebar.left - 0.5 && navRect.right <= sidebar.right + 0.5, where + ': tab row overflows the panel');
        const tabs = [...nav.querySelectorAll('button')];
        const boxes = tabs.map(tab => tab.getBoundingClientRect());
        for (let i = 0; i < tabs.length; i++) {
          assert.ok(tabs[i].scrollWidth <= tabs[i].clientWidth + 1, `${where}: "${tabs[i].textContent}" is clipped in its tab`);
          for (let j = i + 1; j < tabs.length; j++) {
            assert.ok(!intersects(boxes[i], boxes[j]), `${where}: "${tabs[i].textContent}" overlaps "${tabs[j].textContent}"`);
          }
        }
        // The active underline is the tab's own bottom border, so it spans
        // exactly the active label's box.
        const active = nav.querySelector('button.active');
        assert.equal(document.defaultView.getComputedStyle(active).borderBottomStyle, 'solid', where + ': active underline');
        // ...and the scrolling row must not clip it.
        assert.ok(active.getBoundingClientRect().bottom <= navRect.bottom + 0.5, where + ': active underline clipped by the tab row');
      }
    }
  }
});

test('a tab row that does not fit scrolls inside the panel with a faded edge', () => {
  const document = render('de', 200, 'Georgia, serif');
  const nav = document.querySelector('#side-tabs');
  assert.ok(nav.scrollWidth > nav.clientWidth, 'fixture should overflow');
  assert.equal(document.defaultView.getComputedStyle(nav).overflowX, 'auto');
  nav.classList.add('fade-r');
  const mask = document.defaultView.getComputedStyle(nav).maskImage || document.defaultView.getComputedStyle(nav).webkitMaskImage;
  assert.ok(String(mask).includes('gradient'), 'fade edge mask: ' + mask);
});
