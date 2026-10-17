'use strict';
import {readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';

// Load the actual summary helper without a browser, DOM renderer or fixture
// data. The baseline records the original line-splitting behavior exactly.
const transcriptPath = sourcePath(testDir, '../../internal/webgui/assets/js/04-transcript.js');
function load(source = readSource(transcriptPath, 'utf8')) {
  const tools = source.slice(source.indexOf('var DIFF_TOOLS ='), source.indexOf('var FILE_MUTATION_TOOLS ='));
  const start = source.indexOf('function toolChangeStats(');
  const end = source.indexOf('\n// Replayed tool rows', start);
  if (!tools || start < 0 || end < 0) throw Error('Cannot locate tool-change summary source');
  const context = createSandbox({});
  runScript(tools + source.slice(start, end), context);
  return {context, stats: context.toolChangeStats};
}
function baseline(name, text) {
  if (!['edit_line', 'edit_lines', 'insert_after', 'delete_lines', 'apply_patch', 'patch'].includes(name)) {
    return {added: 0, removed: 0, diff: false};
  }
  const stats = {added: 0, removed: 0, diff: false};
  String(text || '').split(/\r?\n/).forEach(line => {
    if (/^\+(?!\+\+)/.test(line)) {stats.added++; stats.diff = true;}
    else if (/^-(?!---)/.test(line)) {stats.removed++; stats.diff = true;}
  });
  return stats;
}
export {load, baseline};
