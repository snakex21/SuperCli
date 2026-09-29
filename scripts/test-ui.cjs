const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const tests = fs.readdirSync(path.join(root, 'test/ui')).filter(name => name.endsWith('.test.cjs')).sort().map(name => path.join(root, 'test/ui', name));
if (!tests.length) throw new Error('No UI tests found');
const result = spawnSync(process.execPath, ['--test', ...tests], { cwd: root, stdio: 'inherit', windowsHide: true });
if (result.error) throw result.error;
process.exit(result.status === null ? 1 : result.status);
