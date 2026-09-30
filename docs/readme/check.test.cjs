const assert = require('node:assert/strict');
const test = require('node:test');
const path = require('node:path');
const { render } = require('./generate.cjs');
const { checkDocument, checkSources } = require('./check.cjs');
const file = path.join(__dirname, 'en.md');
const original = render('en', file);

test('all translations preserve technical literals and destinations', () => checkSources());
test('canonical document passes', () => checkDocument('en', original, file));
test('Windows checkout line endings preserve document parity', () => checkDocument('en', original.replaceAll('\n', '\r\n'), file));
test('CRLF still rejects a changed code example', () => assert.throws(() => checkDocument('en', original.replace('go test ./...', 'go test ./cmd/...').replaceAll('\n', '\r\n'), file)));
for (const [name, mutate] of [
  ['missing paragraph', value => value.replace(/<!-- readme-unit:intro -->\n[^\n]+\n\n/, '')],
  ['duplicate unit', value => `${value}\n<!-- readme-unit:intro -->\nRepeated content.\n`],
  ['unmapped paragraph', value => `${value}\nUntranslated extra paragraph.\n`],
  ['changed heading level', value => value.replace('## Get started', '### Get started')],
  ['changed code example', value => value.replace('go test ./...', 'go test ./cmd/...')],
  ['wrong link destination', value => value.replace('(../quickstart.md)', '(../architecture.md)')],
  ['changed command table cell', value => value.replace('`/resume`, `/export`', '`/resume`, `/quit`')]
]) {
  test(`rejects ${name}`, () => assert.throws(() => checkDocument('en', mutate(original), file)));
}
