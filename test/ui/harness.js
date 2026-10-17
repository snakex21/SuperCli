// Browser-only test support. Source reads are deliberately confined to one snapshot.
const response = await fetch('/__test/sources.json');
if (!response.ok) throw new Error(`Source snapshot: HTTP ${response.status}`);
const snapshot = Object.freeze(Object.assign(Object.create(null), await response.json()));

export const testDir = 'test/ui';
export function sourcePath(...parts) {
  const result = [];
  for (const part of parts) {
    for (const segment of String(part).replaceAll('\\', '/').split('/')) {
      if (!segment || segment === '.') continue;
      if (segment === '..') {
        if (!result.length) throw new Error('Source path escapes the repository');
        result.pop();
      } else result.push(segment);
    }
  }
  return result.join('/');
}
export function readSource(path, encoding = 'utf8') {
  if (!['utf8', 'utf-8', undefined, null].includes(encoding)) {
    throw new Error(`Unsupported source encoding: ${encoding}`);
  }
  const key = sourcePath(path);
  if (!Object.hasOwn(snapshot, key)) throw new Error(`Source not in snapshot: ${key}`);
  if (typeof snapshot[key] !== 'string') throw new TypeError(`Non-text snapshot entry: ${key}`);
  return snapshot[key];
}
export function readDir(path) {
  const directory = sourcePath(path);
  const prefix = directory ? directory + '/' : '';
  const names = new Set();
  for (const key of Object.keys(snapshot)) {
    if (key.startsWith(prefix)) names.add(key.slice(prefix.length).split('/')[0]);
  }
  if (!names.size) throw new Error(`Directory not in snapshot: ${directory}`);
  return [...names].sort();
}

const tag = value => Object.prototype.toString.call(value);
const enumerableKeys = value => Reflect.ownKeys(value).filter(key =>
  Object.prototype.propertyIsEnumerable.call(value, key));
function sameValue(a, b, pairs = []) {
  if (Object.is(a, b)) return true;
  if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') return false;
  const type = tag(a);
  if (type !== tag(b)) return false;
  for (const [left, right] of pairs) {
    if (left === a || right === b) return left === a && right === b;
  }
  pairs.push([a, b]);
  if (type === '[object Date]' && !Object.is(a.getTime(), b.getTime())) return false;
  if (type === '[object RegExp]' && (a.source !== b.source || a.flags !== b.flags || a.lastIndex !== b.lastIndex)) return false;
  if (type === '[object Array]' && a.length !== b.length) return false;
  if (['[object Number]', '[object String]', '[object Boolean]', '[object BigInt]', '[object Symbol]'].includes(type) &&
      !Object.is(a.valueOf(), b.valueOf())) return false;
  if (type === '[object Error]' && (a.name !== b.name || a.message !== b.message ||
      Object.hasOwn(a, 'cause') !== Object.hasOwn(b, 'cause') || !sameValue(a.cause, b.cause, pairs))) return false;
  if (type === '[object ArrayBuffer]' || type === '[object SharedArrayBuffer]' || ArrayBuffer.isView(a)) {
    if (a.byteLength !== b.byteLength) return false;
    const left = ArrayBuffer.isView(a) ? new Uint8Array(a.buffer, a.byteOffset, a.byteLength) : new Uint8Array(a);
    const right = ArrayBuffer.isView(b) ? new Uint8Array(b.buffer, b.byteOffset, b.byteLength) : new Uint8Array(b);
    if (!left.every((value, index) => value === right[index])) return false;
  } else if (type === '[object Map]' || type === '[object Set]') {
    if (a.size !== b.size) return false;
    const remaining = [...b];
    for (const entry of a) {
      let found = -1;
      for (let i = 0; i < remaining.length; i++) {
        const mark = pairs.length;
        const match = type === '[object Map]' ?
          sameValue(entry[0], remaining[i][0], pairs) && sameValue(entry[1], remaining[i][1], pairs) :
          sameValue(entry, remaining[i], pairs);
        if (match) { found = i; break; }
        pairs.length = mark;
      }
      if (found < 0) return false;
      remaining.splice(found, 1);
    }
  } else if (!['[object Object]', '[object Array]', '[object Date]', '[object RegExp]', '[object Error]',
    '[object Number]', '[object String]', '[object Boolean]', '[object BigInt]', '[object Symbol]'].includes(type)) {
    // Promises, weak collections and DOM nodes have identity, not enumerable structural equality.
    return false;
  }
  if (type === '[object Object]') {
    const left = Object.getPrototypeOf(a), right = Object.getPrototypeOf(b);
    const plain = prototype => prototype === null || Object.getPrototypeOf(prototype) === null;
    if ((!plain(left) || !plain(right)) && left?.constructor?.name !== right?.constructor?.name) return false;
  }
  const leftKeys = enumerableKeys(a), rightKeys = enumerableKeys(b);
  return leftKeys.length === rightKeys.length && leftKeys.every(key =>
    rightKeys.includes(key) && sameValue(a[key], b[key], pairs));
}
function display(value) {
  if (typeof value === 'string') return JSON.stringify(value);
  if (typeof value === 'number' && Object.is(value, -0)) return '-0';
  try {
    const seen = new WeakSet();
    const json = JSON.stringify(value, (_key, item) => {
      if (typeof item === 'bigint') return String(item) + 'n';
      if (item && typeof item === 'object') {
        if (seen.has(item)) return '[Circular]';
        seen.add(item);
        if (tag(item) === '[object Map]') return {Map: [...item]};
        if (tag(item) === '[object Set]') return {Set: [...item]};
      }
      return item;
    });
    return (json ?? String(value)).slice(0, 3000);
  } catch { return String(value); }
}
class AssertionError extends Error {
  constructor(message, actual, expected) {
    super(message);
    this.name = 'AssertionError';
    this.actual = actual;
    this.expected = expected;
  }
}
function failure(message, actual, expected, fallback) {
  throw new AssertionError(message || fallback, actual, expected);
}
export function assert(value, message) {
  if (!value) failure(message, value, true, `Expected a truthy value, got ${display(value)}`);
}
assert.ok = assert;
assert.fail = message => failure(message, undefined, undefined, 'Explicit assertion failure');
assert.equal = (actual, expected, message) => {
  if (!Object.is(actual, expected)) failure(message, actual, expected, `Expected ${display(actual)} to equal ${display(expected)}`);
};
assert.notEqual = (actual, expected, message) => {
  if (Object.is(actual, expected)) failure(message, actual, expected, `Expected values to differ: ${display(actual)}`);
};
assert.deepEqual = (actual, expected, message) => {
  if (!sameValue(actual, expected)) failure(message, actual, expected, `Expected deep equality:\n${display(actual)}\n${display(expected)}`);
};
assert.notDeepEqual = (actual, expected, message) => {
  if (sameValue(actual, expected)) failure(message, actual, expected, `Expected structurally different values: ${display(actual)}`);
};
function matches(value, regexp) {
  if (tag(regexp) !== '[object RegExp]') throw new TypeError('Expected a regular expression');
  const previous = regexp.lastIndex;
  try { regexp.lastIndex = 0; return regexp.test(value); }
  finally { regexp.lastIndex = previous; }
}
assert.match = (actual, expected, message) => {
  if (typeof actual !== 'string' || !matches(actual, expected)) {
    failure(message, actual, expected, `Expected ${display(actual)} to match ${String(expected)}`);
  }
};
assert.doesNotMatch = (actual, expected, message) => {
  if (typeof actual !== 'string' || matches(actual, expected)) {
    failure(message, actual, expected, `Expected ${display(actual)} not to match ${String(expected)}`);
  }
};
function expectedError(error, expected) {
  if (expected === undefined) return true;
  if (tag(expected) === '[object RegExp]') return matches(String(error?.message ?? error), expected);
  if (typeof expected === 'function') {
    if (expected === Error || expected.prototype && (tag(expected.prototype) === '[object Error]' || /Error$/.test(expected.name))) {
      return error instanceof expected || tag(error) === '[object Error]' && (expected === Error || error.name === expected.name);
    }
    return expected(error) === true;
  }
  if (expected && typeof expected === 'object') {
    return Reflect.ownKeys(expected).every(key => tag(expected[key]) === '[object RegExp]' ?
      matches(String(error?.[key]), expected[key]) : sameValue(error?.[key], expected[key]));
  }
  throw new TypeError('Expected an error constructor, predicate, regular expression or object');
}
assert.throws = (fn, expected, message) => {
  if (typeof expected === 'string') { message = expected; expected = undefined; }
  let thrown = false, error;
  try { fn(); } catch (caught) { thrown = true; error = caught; }
  if (!thrown || !expectedError(error, expected)) {
    failure(message, error, expected, thrown ? `Unexpected exception: ${String(error)}` : 'Expected an exception');
  }
  return error;
};
assert.rejects = async (operation, expected, message) => {
  if (typeof expected === 'string') { message = expected; expected = undefined; }
  let rejected = false, error;
  try { await (typeof operation === 'function' ? operation() : operation); }
  catch (caught) { rejected = true; error = caught; }
  if (!rejected || !expectedError(error, expected)) {
    failure(message, error, expected, rejected ? `Unexpected rejection: ${String(error)}` : 'Expected a rejection');
  }
  return error;
};
assert.doesNotThrow = (fn, message) => {
  try { return fn(); } catch (error) { failure(message, error, undefined, `Unexpected exception: ${String(error)}`); }
};
assert.doesNotReject = async (operation, message) => {
  try { return await (typeof operation === 'function' ? operation() : operation); }
  catch (error) { failure(message, error, undefined, `Unexpected rejection: ${String(error)}`); }
};
assert.strictEqual = assert.equal;
assert.notStrictEqual = assert.notEqual;
assert.deepStrictEqual = assert.deepEqual;
assert.notDeepStrictEqual = assert.notDeepEqual;
assert.strict = assert;

// A small lexer, not a source-to-source compiler. Literal contents never become declarations.
const identifierStart = character => !!character && /[$_\p{ID_Start}]/u.test(character);
const identifierPart = character => !!character && /[$\u200c\u200d\p{ID_Continue}]/u.test(character);
const expressionKeywords = new Set(['return', 'throw', 'case', 'delete', 'void', 'typeof', 'new', 'in', 'of', 'yield', 'await', 'else', 'do']);
function lex(source) {
  function section(start, interpolation = false) {
    const tokens = [], delimiters = [];
    let i = start, canRegex = true, newline = false;
    const quoted = quote => {
      i++;
      while (i < source.length) {
        if (source[i] === '\\') { i += 2; continue; }
        if (source[i++] === quote) return;
      }
    };
    const template = () => {
      i++;
      while (i < source.length) {
        if (source[i] === '\\') { i += 2; continue; }
        if (source[i] === '`') { i++; return; }
        if (source[i] === '$' && source[i + 1] === '{') i = section(i + 2, true).end;
        else i++;
      }
    };
    while (i < source.length) {
      const character = source[i], next = source[i + 1];
      if (/\s/.test(character)) { newline ||= character === '\n' || character === '\r'; i++; continue; }
      if (character === '/' && next === '/') {
        i += 2;
        while (i < source.length && source[i] !== '\n' && source[i] !== '\r') i++;
        continue;
      }
      if (character === '/' && next === '*') {
        const end = source.indexOf('*/', i + 2);
        const stop = end < 0 ? source.length : end + 2;
        newline ||= /[\r\n]/.test(source.slice(i, stop));
        i = stop;
        continue;
      }
      if (interpolation && character === '}' && !delimiters.length) return {tokens, end: i + 1};
      const begin = i, previous = tokens.at(-1);
      let kind = 'punctuation';
      if (character === '"' || character === "'") { quoted(character); kind = 'string'; canRegex = false; }
      else if (character === '`') { template(); kind = 'template'; canRegex = false; }
      else if (character === '/' && canRegex) {
        i++;
        let inClass = false;
        while (i < source.length) {
          const value = source[i++];
          if (value === '\\') { i++; continue; }
          if (value === '[') inClass = true;
          if (value === ']') inClass = false;
          if (value === '/' && !inClass) break;
        }
        while (identifierPart(source[i])) i++;
        kind = 'regexp'; canRegex = false;
      } else if (identifierStart(character)) {
        i++;
        while (identifierPart(source[i])) i++;
        kind = 'identifier'; canRegex = expressionKeywords.has(source.slice(begin, i));
      } else if (/\d/.test(character)) {
        i++;
        while (i < source.length && /[\w.]/.test(source[i])) i++;
        kind = 'number'; canRegex = false;
      } else {
        const operator = ['===', '!==', '>>>', '**=', '=>', '==', '!=', '<=', '>=', '++', '--', '&&', '||', '??', '?.', '+=', '-=', '*=', '/=', '<<', '>>', '**', '...'].find(value => source.startsWith(value, i));
        i += operator?.length || 1;
        const value = source.slice(begin, i);
        if (['(', '[', '{'].includes(value)) {
          const control = value === '(' && ['if', 'while', 'for', 'with', 'switch', 'catch'].includes(previous?.value);
          const block = value === '{' && (!previous || [';', '}', ')', '=>', 'else', 'try', 'finally', 'do'].includes(previous.value));
          delimiters.push({value, control, block});
          canRegex = true;
        } else if ([')', ']', '}'].includes(value)) {
          const opening = delimiters.pop();
          canRegex = value === ')' ? !!opening?.control : value === '}' && !!opening?.block;
        } else canRegex = !['.', '?.', '++', '--'].includes(value);
      }
      tokens.push({value: source.slice(begin, i), start: begin, end: i, kind, newline});
      newline = false;
    }
    return {tokens, end: i};
  }
  return section(0).tokens;
}
function declarations(tokens) {
  const names = new Map();
  const closing = Object.assign(Object.create(null), {'(': ')', '[': ']', '{': '}'});
  const skipGroup = index => {
    const stack = [closing[tokens[index].value]];
    while (++index < tokens.length && stack.length) {
      const value = tokens[index].value;
      if (closing[value]) stack.push(closing[value]);
      else if (value === stack.at(-1)) stack.pop();
    }
    return index;
  };
  const skipInitializer = index => {
    while (index < tokens.length) {
      const token = tokens[index];
      if ([',', ';', '}', ')', ']'].includes(token.value) || token.newline &&
          ['var', 'let', 'const', 'function', 'class', 'if', 'for', 'while', 'try', 'throw', 'return'].includes(token.value)) break;
      index = closing[token.value] ? skipGroup(index) : index + 1;
    }
    return index;
  };
  function binding(index, kind) {
    const token = tokens[index];
    if (!token) return index;
    if (token.value === '...') return binding(index + 1, kind);
    if (token.kind === 'identifier') { names.set(token.value, kind); return index + 1; }
    if (token.value !== '{' && token.value !== '[') return index + 1;
    const object = token.value === '{', end = object ? '}' : ']';
    index++;
    while (index < tokens.length && tokens[index].value !== end) {
      if (tokens[index].value === ',') { index++; continue; }
      if (object && tokens[index].value !== '...') {
        if (tokens[index].value === '[') index = skipGroup(index);
        else if (tokens[index + 1]?.value !== ':') {
          index = binding(index, kind);
          if (tokens[index]?.value === '=') index = skipInitializer(index + 1);
          continue;
        } else index++;
        if (tokens[index]?.value === ':') index++;
      }
      index = binding(index, kind);
      if (tokens[index]?.value === '=') index = skipInitializer(index + 1);
    }
    return index + 1;
  }
  let statementStart = true;
  for (let i = 0; i < tokens.length;) {
    const token = tokens[i], value = token.value;
    if (['var', 'let', 'const'].includes(value) && (statementStart || token.newline)) {
      const kind = value;
      i++;
      do {
        i = binding(i, kind);
        if (tokens[i]?.value === '=') i = skipInitializer(i + 1);
        if (tokens[i]?.value !== ',') break;
        i++;
      } while (i < tokens.length);
      statementStart = true;
      continue;
    }
    if (statementStart && (value === 'function' || value === 'class' || value === 'async' && tokens[i + 1]?.value === 'function')) {
      if (value === 'async') i++;
      const kind = tokens[i++].value;
      if (tokens[i]?.value === '*') i++;
      if (tokens[i]?.kind === 'identifier') names.set(tokens[i++].value, kind);
      while (i < tokens.length && tokens[i].value !== '{') {
        i = closing[tokens[i].value] ? skipGroup(i) : i + 1;
      }
      if (i < tokens.length) i = skipGroup(i);
      statementStart = true;
      continue;
    }
    if (closing[value]) { i = skipGroup(i); statementStart = value === '{'; continue; }
    statementStart = value === ';' || token.kind === 'string' && (tokens[i + 1]?.newline || tokens[i + 1]?.value === ';');
    i++;
  }
  return names;
}
function directiveEnd(tokens) {
  let index = 0, end = 0;
  while (tokens[index]?.kind === 'string') {
    const next = tokens[index + 1];
    if (next && next.value !== ';' && (!next.newline || ['(', '[', '.', '?.', '+', '-', '/', '*', '**', '==', '===', '!=', '!==', '<', '>', '<=', '>=', '?', ',', '&&', '||', '??'].includes(next.value))) break;
    end = tokens[index].end;
    index++;
    if (tokens[index]?.value === ';') end = tokens[index++].end;
  }
  return end;
}

let activeCase = null, activeScope = null;
const frames = new Set(), frameForWindow = new WeakMap(), sandboxes = new WeakMap();
function asynchronousError(event) {
  const owner = activeCase ?? activeScope;
  if (!owner) return;
  event.preventDefault();
  owner.errors.push(event.reason ?? event.error ?? new Error(event.message || 'Unhandled browser error'));
}
window.addEventListener('error', asynchronousError);
window.addEventListener('unhandledrejection', asynchronousError);
function newFrame(html) {
  const iframe = document.createElement('iframe');
  iframe.dataset.supercliTestFrame = '';
  iframe.tabIndex = -1;
  iframe.setAttribute('aria-hidden', 'true');
  // Do not use display:none, hidden, inert or visibility:hidden: layout and focus are under test.
  iframe.style.cssText = 'position:fixed;left:-10000px;top:0;width:1280px;height:900px;border:0;';
  document.body.appendChild(iframe);
  const realm = iframe.contentWindow;
  const frame = {iframe, realm, owner: activeCase ?? activeScope, states: new Set(), cleanups: []};
  frames.add(frame);
  frameForWindow.set(realm, frame);
  if (html !== undefined) {
    realm.document.open();
    realm.document.write(String(html));
    realm.document.close();
  }
  realm.addEventListener('error', asynchronousError);
  realm.addEventListener('unhandledrejection', asynchronousError);
  return frame;
}
function disposeFrames(owner) {
  const errors = [];
  for (const frame of frames) {
    if (frame.owner !== owner) continue;
    for (const state of frame.states) state.disposed = true;
    for (const cleanup of frame.cleanups) {
      try { cleanup(); } catch (error) { errors.push(error); }
    }
    frame.realm.removeEventListener('error', asynchronousError);
    frame.realm.removeEventListener('unhandledrejection', asynchronousError);
    frame.iframe.remove();
    frames.delete(frame);
  }
  if (errors.length) throw new AggregateError(errors, errors.map(error => String(error?.message ?? error)).join('\n'));
}
export function parseHTML(html) {
  const {realm} = newFrame(html);
  return {window: realm, document: realm.document};
}
const hostMethods = new Set(['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'requestAnimationFrame',
  'cancelAnimationFrame', 'requestIdleCallback', 'cancelIdleCallback', 'queueMicrotask', 'fetch', 'atob', 'btoa',
  'getComputedStyle', 'matchMedia', 'addEventListener', 'removeEventListener', 'dispatchEvent', 'focus', 'blur', 'gc']);
function nativeWindow(value) {
  return value && tag(value) === '[object Window]';
}
export function createSandbox(context = {}) {
  if ((typeof context !== 'object' && typeof context !== 'function') || context === null) {
    throw new TypeError('A sandbox context must be an object');
  }
  const existing = sandboxes.get(context);
  if (existing) {
    if (existing.disposed) throw new Error('Sandbox belongs to a completed test');
    return context;
  }
  const candidate = nativeWindow(context) ? context : nativeWindow(context.window) ? context.window : null;
  const frame = candidate && frameForWindow.get(candidate) || newFrame();
  const realm = frame.realm, bound = new Map();
  for (const [key, value] of [['window', context], ['self', context.window ?? context], ['globalThis', context]]) {
    if (!(key in context)) context[key] = value;
  }
  const scope = new Proxy(context, {
    has(target, key) { return key === 'eval' || Reflect.has(target, key) || Reflect.has(realm, key); },
    get(target, key) {
      if (key === Symbol.unscopables) return undefined;
      if (key === 'eval') return realm.eval;
      const supplied = Reflect.has(target, key);
      const owner = supplied ? target : realm;
      const value = Reflect.get(owner, key, owner);
      if (typeof value === 'function' && hostMethods.has(key) &&
          (!supplied || nativeWindow(target) || value === window[key] || value === realm[key])) {
        if (bound.get(key)?.original !== value) {
          const receiver = supplied && value === window[key] ? window : owner;
          let method = value.bind(receiver);
          const cancelName = {setTimeout: 'clearTimeout', setInterval: 'clearInterval',
            requestAnimationFrame: 'cancelAnimationFrame', requestIdleCallback: 'cancelIdleCallback'}[key];
          if (cancelName) {
            method = (...args) => {
              const id = value.apply(receiver, args);
              frame.cleanups.push(() => receiver[cancelName](id));
              return id;
            };
          }
          bound.set(key, {original: value, method});
        }
        return bound.get(key).method;
      }
      return value;
    },
    set(target, key, value) { return Reflect.set(target, key, value, target); }
  });
  const state = {scope, realm, context, disposed: false};
  frame.states.add(state);
  sandboxes.set(context, state);
  return context;
}
export function runScript(source, context, options = {}) {
  createSandbox(context);
  const state = sandboxes.get(context);
  if (state.disposed) throw new Error('Sandbox belongs to a completed test');
  source = String(source);
  const tokens = lex(source), names = declarations(tokens);
  // The publisher executes after directives but before initializers, including early throws/returns.
  // Its closures capture eval bindings ABOVE with(scope), never properties masquerading as locals.
  const identifiers = new Set(tokens.filter(token => token.kind === 'identifier').map(token => token.value));
  const unique = prefix => {
    while (identifiers.has(prefix)) prefix += '_';
    identifiers.add(prefix);
    return prefix;
  };
  const sourceName = unique('__source'), publishName = unique('__publish'), valueName = unique('__value');
  const evaluate = state.realm.Function('scope',
    `with(scope){return function(${sourceName},${publishName}){return eval(${sourceName})}}`)(state.scope);
  const publish = (name, get, set, kind) => {
    if (kind === 'var' && name in context) set(context[name]);
    Object.defineProperty(context, name, {enumerable: true, configurable: true, get, set});
  };
  const publisher = [...names].map(([name, kind]) =>
    `${publishName}(${JSON.stringify(name)},()=>${name},${valueName}=>${name}=${valueName},${JSON.stringify(kind)});`).join('');
  const end = directiveEnd(tokens);
  const filename = String(options?.filename || 'sandbox.js').replace(/[\r\n\u2028\u2029]/g, '');
  const instrumented = source.slice(0, end) + '\n;' + publisher + '\n' + source.slice(end) + '\n//# sourceURL=' + filename;
  return evaluate.call(context, instrumented, publish);
}
export function runInNewSandbox(source, context = {}, options) {
  return runScript(source, createSandbox(context), options);
}
export class Script {
  constructor(source, options = {}) { this.source = String(source); this.options = options; }
  runInContext(context, options = {}) { return runScript(this.source, context, {...this.options, ...options}); }
  runInNewContext(context = {}, options = {}) { return this.runInContext(createSandbox(context), options); }
}

function newRegistry() { return {cases: [], before: [], after: [], beforeEach: [], afterEach: []}; }
let registration = newRegistry();
function caseArguments(name, options, fn) {
  if (typeof name === 'function') { fn = name; name = fn.name; options = {}; }
  else if (typeof options === 'function') { fn = options; options = {}; }
  if (typeof fn !== 'function') throw new TypeError(`Test ${name} needs a function`);
  return {name: String(name || fn.name || '<anonymous>'), options: options || {}, fn};
}
export function test(name, options, fn) {
  registration.cases.push(caseArguments(name, options, fn));
}
for (const kind of ['before', 'after', 'beforeEach', 'afterEach']) {
  test[kind] = fn => {
    if (typeof fn !== 'function') throw new TypeError(`test.${kind} needs a function`);
    registration[kind].push(fn);
  };
}
for (const kind of ['skip', 'todo', 'only']) {
  test[kind] = (name, options, fn) => {
    const item = caseArguments(name, options, fn);
    item.options = {...item.options, [kind]: true};
    registration.cases.push(item);
  };
}
const turn = () => new Promise(resolve => setTimeout(resolve, 0));
function errorDetails(name, error) {
  return {name, error: String(error?.message ?? error), stack: String(error?.stack ?? '')};
}
function moduleURL(file) {
  if (/^(?:[a-z][a-z\d+.-]*:|\/)/i.test(file)) return new URL(file, location.href).href;
  return new URL(file.includes('/') && !file.startsWith('.') ? '/' + file : file, import.meta.url).href;
}
export async function run(files) {
  const outerRegistry = registration, outerCase = activeCase, outerScope = activeScope;
  const registry = registration = newRegistry();
  const scope = activeScope = {errors: []};
  activeCase = null;
  // Include registrations made by explicitly imported modules before run([]), too.
  if (!outerCase) {
    registry.cases.push(...outerRegistry.cases.splice(0));
    for (const kind of ['before', 'after', 'beforeEach', 'afterEach']) registry[kind].push(...outerRegistry[kind]);
  }
  const result = {total: 0, passed: 0, skipped: 0, tests: [], failures: []};
  function recordErrors(name, errors) {
    if (!errors.length) return;
    const details = errors.map(error => errorDetails(name, error));
    result.failures.push({name, error: details.map(detail => detail.error).join('\n'),
      stack: details.map(detail => detail.stack).join('\n\n')});
  }
  function record(name, status, start) {
    result.tests.push({name, status, duration_ms: Math.max(0, performance.now() - start)});
    result.total++;
    if (status === 'passed') result.passed++;
    if (status === 'skipped') result.skipped++;
  }
  async function suitePhase(name, operation) {
    const start = performance.now();
    try { await operation(); } catch (error) { scope.errors.push(error); }
    // Even a hook/import that returns immediately can leave an unhandled rejection
    // queued behind the first timer task. Keep the suite owner active through both.
    await turn();
    await turn();
    const errors = scope.errors.splice(0);
    if (errors.length) {
      recordErrors(name, errors);
      record(name, 'failed', start);
    }
  }
  async function execute(item, parent = null) {
    const name = parent ? parent.name + ' > ' + item.name : item.name;
    const start = performance.now();
    if (item.options.skip) { record(name, 'skipped', start); return true; }
    const previous = activeCase;
    const state = {name, parent, errors: [], beforeEach: [], afterEach: [], after: [], children: [], chain: Promise.resolve()};
    const controller = new AbortController();
    const ancestors = [];
    for (let ancestor = parent; ancestor; ancestor = ancestor.parent) ancestors.unshift(ancestor);
    let skipped = false, timer;
    const context = {
      name, signal: controller.signal, assert,
      diagnostic(message) { console.info(`[${name}] ${String(message)}`); },
      skip() { skipped = true; },
      before(fn) { return Promise.resolve().then(() => fn(context)); },
      after(fn) { state.after.push(fn); },
      beforeEach(fn) { state.beforeEach.push(fn); },
      afterEach(fn) { state.afterEach.push(fn); },
      test(childName, options, fn) {
        const child = caseArguments(childName, options, fn);
        const pending = state.chain.then(() => execute(child, state));
        state.chain = pending;
        state.children.push(pending);
        return pending;
      }
    };
    const body = async () => {
      for (const hook of [...registry.beforeEach, ...ancestors.flatMap(ancestor => ancestor.beforeEach)]) await hook(context);
      await item.fn(context);
      const children = await Promise.all(state.children);
      if (children.some(passed => !passed)) throw new Error('A subtest failed');
    };
    activeCase = state;
    try {
      const operation = body();
      if (Number.isFinite(item.options.timeout) && item.options.timeout > 0) {
        await Promise.race([operation, new Promise((_resolve, reject) => {
          timer = setTimeout(() => reject(new Error(`Test timed out after ${item.options.timeout} ms`)), item.options.timeout);
        })]);
      } else await operation;
    } catch (error) { state.errors.push(error); }
    finally {
      clearTimeout(timer);
      const hooks = [...state.after].reverse().concat(
        [...ancestors].reverse().flatMap(ancestor => [...ancestor.afterEach].reverse()), [...registry.afterEach].reverse());
      for (const hook of hooks) {
        try { await hook(context); } catch (error) { state.errors.push(error); }
      }
      await turn();
      await turn(); // Rejection reporting can be queued behind the first timer task.
      controller.abort();
      try { disposeFrames(state); } catch (error) { state.errors.push(error); }
      activeCase = previous;
    }
    recordErrors(name, state.errors);
    const status = state.errors.length ? 'failed' : skipped ? 'skipped' : 'passed';
    record(name, status, start);
    return status !== 'failed';
  }
  try {
    for (const file of files) await suitePhase('import ' + file, () => import(moduleURL(String(file))));
    for (const hook of registry.before) await suitePhase('before', hook);
    for (const item of registry.cases) await execute(item);
    for (const hook of [...registry.after].reverse()) await suitePhase('after', hook);
  } finally {
    try { await suitePhase('cleanup', () => disposeFrames(scope)); }
    finally {
      registration = outerRegistry;
      activeCase = outerCase;
      activeScope = outerScope;
    }
  }
  return result;
}
