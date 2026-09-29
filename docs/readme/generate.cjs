const fs = require('node:fs');
const path = require('node:path');
const source = require('./source.json');
const translations = Object.fromEntries(Object.keys(source.locales).filter(code => code !== 'en').map(code => {
  const file = path.join(__dirname, 'translations', `${code}.json`);
  return [code, fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : null];
}));
const root = path.resolve(__dirname, '../..');
const ids = source.text.map(([id]) => id);

function valuesFor(locale) {
  if (locale === 'en') return Object.fromEntries(source.text);
  const values = translations[locale];
  if (!Array.isArray(values) || values.length !== ids.length) {
    throw new Error(`${locale}: expected ${ids.length} translated units, got ${values?.length}`);
  }
  return Object.fromEntries(ids.map((id, i) => [id, values[i]]));
}

function render(locale, file) {
  const values = valuesFor(locale);
  const rel = target => path.relative(path.dirname(file), path.resolve(root, target)).replaceAll('\\', '/');
  const text = id => values[id].replace(/\{([a-z]+)\}/g, (_, key) => {
    const target = source.links[key];
    if (!target) throw new Error(`Unknown link ${key}`);
    return /^https?:/.test(target) ? target : rel(target);
  }).replaceAll('|', '\\|');
  const nav = Object.entries(source.locales).map(([code, label]) => `[${label}](${rel(`docs/readme/${code}.md`)})`).join(' · ');
  const body = source.blocks.map(block => {
    if (typeof block === 'string') {
      return `<!-- readme-unit:${block} -->\n${block.startsWith('h.') ? '## ' : ''}${text(block)}`;
    }
    if (block.type === 'title') return `# ${block.text}`;
    if (block.type === 'reference') return `[${source.links[block.key]}](${rel(source.links[block.key])})`;
    if (block.type === 'code') return `\`\`\`${block.lang}\n${block.text}\n\`\`\``;
    if (block.type === 'table') {
      return `<!-- readme-unit:${[...block.headers, ...block.rows.map(([, id]) => id)].join(',')} -->\n| ${block.headers.map(text).join(' | ')} |\n| --- | --- |\n` + block.rows.map(([literal, id]) => `| ${literal.replaceAll('|', '\\|')} | ${text(id)} |`).join('\n');
    }
    throw new Error(`Unknown block ${block.type}`);
  }).join('\n\n');
  return `${nav}\n\n<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run node docs/readme/generate.cjs. -->\n\n${body}\n`;
}

function generate() {
  for (const locale of Object.keys(source.locales)) {
    const file = path.join(__dirname, `${locale}.md`);
    fs.writeFileSync(file, render(locale, file), 'utf8');
  }
  fs.writeFileSync(path.join(root, 'README.md'), render('en', path.join(root, 'README.md')), 'utf8');
}

if (require.main === module) {
  if (process.argv.includes('--english')) {
    for (const file of [path.join(root, 'README.md'), path.join(__dirname, 'en.md')]) fs.writeFileSync(file, render('en', file), 'utf8');
    console.log('Generated English master.');
  } else { generate(); console.log(`Generated English README and ${Object.keys(source.locales).length} language editions.`); }
}
module.exports = { render, valuesFor, generate, root, ids, source, translations };
