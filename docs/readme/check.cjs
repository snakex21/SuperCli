const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { render, valuesFor, source, translations, root, ids } = require('./generate.cjs');
const expectedLocales = 'en bg cs da de el es et fi fr hr hu it lt lv nb nl pl pt-BR ro ru sk sl sr-Latn sv tr uk'.split(' ');

const literals = value => [...value.matchAll(/`([^`]+)`/g)].map(match => match[1]);
const placeholders = value => [...value.matchAll(/\{([a-z]+)\}/g)].map(match => match[1]);
const unitIds = value => [...value.matchAll(/<!-- readme-unit:([^>]+) -->/g)].flatMap(match => match[1].trim().split(','));
const codeBlocks = value => [...value.matchAll(/^```([^\n]*)\n([\s\S]*?)^```$/gm)].map(match => [match[1], match[2]]);
const headings = value => value.replace(/^```[^\n]*\n[\s\S]*?^```$/gm, '').split('\n').filter(line => /^#{1,6} /.test(line)).map(line => line.match(/^#+/)[0]);
function links(value, file) {
  return [...value.matchAll(/\[[^\]]+\]\(([^)]+)\)/g)].map(([, target]) => {
    if (/^https?:/.test(target)) return target;
    const resolved = path.resolve(path.dirname(file), target);
    assert.ok(fs.existsSync(resolved), `${file}: missing linked file ${target}`);
    return path.relative(root, resolved).replaceAll('\\', '/');
  });
}

function checkSources() {
  assert.deepEqual(Object.keys(source.locales), expectedLocales, 'Exact language inventory and order');
  assert.equal(new Set(ids).size, ids.length, 'Unique English content IDs');
  assert.deepEqual(Object.keys(translations), expectedLocales.slice(1), 'Complete translation inventory');
  for (const locale of expectedLocales.slice(1)) {
    const values = valuesFor(locale);
    for (const [id, english] of source.text) {
      const value = values[id];
      assert.equal(typeof value, 'string', `${locale}/${id}: translated string required`);
      assert.ok(value.trim(), `${locale}/${id}: empty translation`);
      assert.ok(!value.includes('\n'), `${locale}/${id}: each paragraph must remain one mapped unit`);
      assert.deepEqual(literals(value), literals(english), `${locale}/${id}: technical literals differ`);
      assert.deepEqual(placeholders(value), placeholders(english), `${locale}/${id}: link destinations differ`);
      assert.deepEqual([...value.matchAll(/\b\d+(?:\.\d+)*\b/g)].map(m => m[0]), [...english.matchAll(/\b\d+(?:\.\d+)*\b/g)].map(m => m[0]), `${locale}/${id}: numeric facts differ`);
      if (english.length > 60) assert.notEqual(value, english, `${locale}/${id}: untranslated English paragraph`);
    }
  }
}

function checkDocument(locale, content, file) {
  const englishFile = path.join(__dirname, 'en.md');
  const english = render('en', englishFile);
  assert.deepEqual(unitIds(content), ids, `${locale}: missing, duplicate, or reordered content units`);
  assert.deepEqual(headings(content), headings(english), `${locale}: heading structure differs`);
  assert.deepEqual(codeBlocks(content), codeBlocks(english), `${locale}: code examples differ`);
  assert.deepEqual(links(content, file), links(english, englishFile), `${locale}: resolved links differ`);
  assert.equal(content, render(locale, file), `${locale}: generated text differs; check unmapped or changed paragraphs and regenerate`);
}

function check() {
  checkSources();
  for (const locale of expectedLocales) {
    const file = path.join(__dirname, `${locale}.md`);
    checkDocument(locale, fs.readFileSync(file, 'utf8'), file);
  }
  const file = path.join(root, 'README.md');
  checkDocument('en', fs.readFileSync(file, 'utf8'), file);
  console.log(`README parity passed: ${expectedLocales.length} languages, ${ids.length} translated units each, ${codeBlocks(render('en', file)).length} identical code blocks, all link targets valid.`);
}

if (require.main === module) check();
module.exports = { check, checkSources, checkDocument };
