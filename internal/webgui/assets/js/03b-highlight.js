"use strict";

/* ═══ fenced code-block syntax highlighter (dependency-free, XSS-safe) ═══
   Safety: every emitted character goes through escHtml(); the only markup
   produced is <span class="hl-…"> with a class name fixed in this file.
   Performance: all token patterns are sticky and either always succeed
   (optional closing delimiter, consumed once) or use bounded repetition, so a
   scan is linear even on hostile/unterminated input. Blocks above
   HL_MAX_CHARS stay plain, and larger results are cached by content because
   history re-renders call renderMarkdownish for the same blocks again. */

var HL_MAX_CHARS = 200000;
var HL_CACHE_MIN = 512;          // tiny blocks are cheaper to re-scan than to key
var HL_CACHE_MAX = 64;
var HL_CACHE_CHARS = 4000000;    // bound memory held by cached code + HTML
var hlCache = new Map(), hlCacheChars = 0;

function hlWords(s, lower) {
  var set = Object.create(null);
  String(s || "").split(" ").forEach(function (w) { if (w) set[lower ? w.toLowerCase() : w] = true; });
  return set;
}
function hlSpan(cls, text) { return '<span class="hl-' + cls + '">' + escHtml(text) + "</span>"; }
function hlLineStart(code, pos) {
  for (var i = pos - 1; i >= 0; i--) {
    var c = code.charCodeAt(i);
    if (c === 10) return true;
    if (c !== 32 && c !== 9 && c !== 13) return false;
  }
  return true;
}
function hlAfterSpace(code, pos) { return !pos || " \t\r\n;|&(".indexOf(code.charAt(pos - 1)) >= 0; }
function hlNextChar(code, end) {
  while (end < code.length) {
    var c = code.charAt(end);
    if (c !== " " && c !== "\t") return c;
    end++;
  }
  return "";
}

var HL_RE = {
  lineSlash: /\/\/.*/y,
  block: /\/\*[\s\S]*?(?:\*\/|$)/y,
  hash: /#.*/y,
  dq: /"(?:[^"\\\n]|\\[\s\S])*"?/y,
  sq: /'(?:[^'\\\n]|\\[\s\S])*'?/y,
  dqMulti: /"(?:[^"\\]|\\[\s\S])*"?/y,
  tpl: /`(?:[^`\\]|\\[\s\S])*`?/y,
  raw: /`[^`]*`?/y,
  char: /'(?:[^'\\\n]|\\[^\n]){1,10}'/y,
  num: /(?:0[xX][\da-fA-F_]+|0[bB][01_]+|0[oO][0-7_]+|\d[\d_]*(?:\.\d[\d_]*)?(?:[eE][+-]?\d+)?)[A-Za-z]{0,6}/y,
  annot: /@[A-Za-z_][\w.]*/y,
  word: /[A-Za-z_$\u00c0-\ud7ff\ue000-\uffff][\w$\u00c0-\ud7ff\ue000-\uffff]*/y,
  dashWord: /[A-Za-z_][\w-]*/y,
  // Characters that never start a token; consumed in runs so rules are not
  // retried at every whitespace/punctuation character.
  inert: /[\s(),;\[\]=+*%&|^~!?<>:{}]+/y
};

function hlRule(cls, re, when) { return {cls: cls, re: re, when: when}; }

function hlClassifier(o) {
  var kw = hlWords(o.kw, o.ci), lit = hlWords(o.lit, o.ci), types = hlWords(o.types, o.ci);
  var fns = hlWords(o.fns, o.ci), vars = hlWords(o.vars, o.ci);
  return function (w, code, end) {
    if (w.length > 80) return null;
    var k = o.ci ? w.toLowerCase() : w;
    if (kw[k]) return "kw";
    if (lit[k]) return "lit";
    if (types[k]) return "type";
    if (vars[k]) return "var";
    var next = code.charCodeAt(end);
    if (next === 40 || fns[k]) return "fn";
    if (o.macro && next === 33 && code.charCodeAt(end + 1) !== 61) return "fn";
    if (o.verbNoun && /^[A-Za-z]+-[A-Za-z]+$/.test(w)) return "fn";
    var first = w.charCodeAt(0);
    if (o.caps && first >= 65 && first <= 90 && /[a-z]/.test(w)) return "type";
    return null;
  };
}
function hlLang(rules, o, word, inert) {
  return {rules: rules, classify: o.classify || hlClassifier(o), word: word || HL_RE.word, inert: inert || HL_RE.inert};
}

function hlRun(code, spec) {
  var out = [], plain = "", pos = 0, n = code.length, rules = spec.rules, ctx = {depth: 0};
  function push(cls, text) {
    if (!cls) { plain += text; return; }
    if (plain) { out.push(escHtml(plain)); plain = ""; }
    out.push(hlSpan(cls, text));
  }
  while (pos < n) {
    var m = null, cls = null;
    for (var i = 0; i < rules.length; i++) {
      var r = rules[i];
      r.re.lastIndex = pos;
      m = r.re.exec(code);
      if (m && m[0] && (!r.when || r.when(code, pos, ctx))) {
        cls = typeof r.cls === "function" ? r.cls(m[0], code, pos + m[0].length, ctx) : r.cls;
        break;
      }
      m = null;
    }
    if (!m) {
      spec.word.lastIndex = pos;
      m = spec.word.exec(code);
      if (m) cls = spec.classify(m[0], code, pos + m[0].length, ctx);
    }
    if (!m) { spec.inert.lastIndex = pos; m = spec.inert.exec(code); }
    var text = m ? m[0] : code.charAt(pos);
    push(cls, text);
    pos += text.length;
  }
  if (plain) out.push(escHtml(plain));
  return out.join("");
}

var HL_C_KW = "auto break case const continue default do else enum extern for goto if inline register restrict return sizeof static struct switch typedef union volatile while alignas alignof asm catch class constexpr const_cast decltype delete dynamic_cast explicit export friend mutable namespace new noexcept operator private protected public reinterpret_cast static_assert static_cast template this throw try typeid typename using virtual override final co_await co_return co_yield concept requires";
var HL_C_TYPES = "void char short int long float double signed unsigned bool size_t ssize_t ptrdiff_t int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t uintptr_t wchar_t char16_t char32_t";
var HL_JAVA_KW = "abstract assert break case catch class const continue default do else enum extends final finally for goto if implements import instanceof interface native new package private protected public return static strictfp super switch synchronized this throw throws transient try var volatile while record sealed permits yield";
var HL_JAVA_TYPES = "boolean byte char double float int long short void String Object Integer Long Double Boolean";
var HL_CLIKE_COMMON = [hlRule("com", HL_RE.lineSlash), hlRule("com", HL_RE.block), hlRule("str", HL_RE.dq)];

var HL_BUILD = {
  go: function () {
    return hlLang([hlRule("com", HL_RE.lineSlash), hlRule("com", HL_RE.block), hlRule("str", HL_RE.dq), hlRule("str", HL_RE.raw), hlRule("str", HL_RE.char), hlRule("num", HL_RE.num)], {
      kw: "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var",
      lit: "true false nil iota",
      types: "any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr"
    });
  },
  js: function () {
    return hlLang([hlRule("com", HL_RE.lineSlash), hlRule("com", HL_RE.block), hlRule("str", HL_RE.dq), hlRule("str", HL_RE.sq), hlRule("str", HL_RE.tpl), hlRule("num", HL_RE.num), hlRule("meta", HL_RE.annot)], {
      kw: "abstract as async await break case catch class const continue debugger declare default delete do else enum export extends finally for from function get if implements import in infer instanceof interface keyof let namespace new of package private protected public readonly return satisfies set static super switch this throw try type typeof var void while with yield",
      lit: "true false null undefined NaN Infinity",
      types: "any bigint boolean never number object string symbol unknown",
      caps: true
    });
  },
  json: function () {
    return hlLang([hlRule("com", HL_RE.lineSlash), hlRule("com", HL_RE.block),
      hlRule(function (text, code, end) { return hlNextChar(code, end) === ":" ? "attr" : "str"; }, HL_RE.dq),
      hlRule("num", /-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/y)], {lit: "true false null"});
  },
  python: function () {
    var pre = "[rRbBuUfF]{0,2}";
    return hlLang([hlRule("com", HL_RE.hash),
      hlRule("str", new RegExp(pre + '"""[\\s\\S]*?(?:"""|$)', "y")), hlRule("str", new RegExp(pre + "'''[\\s\\S]*?(?:'''|$)", "y")),
      hlRule("str", new RegExp(pre + '"(?:[^"\\\\\\n]|\\\\[\\s\\S])*"?', "y")), hlRule("str", new RegExp(pre + "'(?:[^'\\\\\\n]|\\\\[\\s\\S])*'?", "y")),
      hlRule("num", HL_RE.num), hlRule("meta", HL_RE.annot, hlLineStart)], {
      kw: "and as assert async await break case class continue def del elif else except finally for from global if import in is lambda match nonlocal not or pass raise return try while with yield",
      lit: "True False None",
      types: "bool bytes dict float int list object set str tuple type",
      vars: "self cls",
      caps: true
    });
  },
  shell: function () {
    return hlLang([hlRule("com", HL_RE.hash, hlAfterSpace), hlRule("str", /'[^']*'?/y), hlRule("str", HL_RE.dqMulti),
      hlRule("var", /\$(?:\{[^}\n]*\}?|[A-Za-z_]\w*|[0-9#?$!@*-])/y), hlRule("attr", /--?[A-Za-z][\w-]*/y, hlAfterSpace), hlRule("num", HL_RE.num)], {
      kw: "if then else elif fi for while until do done case esac in function select return exit break continue local export readonly declare typeset set unset shift source alias unalias eval exec trap time",
      fns: "echo printf cd pwd ls cat grep sed awk find mkdir rm cp mv chmod chown curl wget git go npm node python python3 pip docker make sudo test read kill ps tar ssh"
    }, HL_RE.dashWord);
  },
  powershell: function () {
    return hlLang([hlRule("com", /<#[\s\S]*?(?:#>|$)/y), hlRule("com", HL_RE.hash),
      hlRule("str", /@"[\s\S]*?(?:\n"@|$)/y), hlRule("str", /@'[\s\S]*?(?:\n'@|$)/y),
      hlRule("str", /"(?:[^"`]|`[\s\S])*"?/y), hlRule("str", /'(?:[^']|'')*'?/y),
      hlRule(function (text) { return /^\$(?:true|false|null)$/i.test(text) ? "lit" : "var"; }, /\$(?:\{[^}\n]*\}?|[\w:?]+)/y),
      hlRule("type", /\[[A-Za-z_][\w.]*(?:\[\])?\]/y), hlRule("attr", /-[A-Za-z]\w*/y, hlAfterSpace), hlRule("num", HL_RE.num)], {
      kw: "begin break catch class continue data define do dynamicparam else elseif end enum exit filter finally for foreach from function hidden if in param process return static switch throw trap try until using var while workflow",
      ci: true, verbNoun: true
    }, HL_RE.dashWord, /[\s(),;=+*%&|^~!?>:{}\]]+/y);
  },
  sql: function () {
    return hlLang([hlRule("com", /--.*/y), hlRule("com", HL_RE.block), hlRule("str", /'(?:[^']|'')*'?/y), hlRule("attr", /"(?:[^"]|"")*"?/y),
      hlRule("attr", /`[^`\n]*`?/y), hlRule("num", HL_RE.num), hlRule("var", /@[\w$#]+|\$\d+/y)], {
      kw: "add all alter analyze and as asc begin between by cascade case check column commit constraint create cross database declare default delete desc distinct drop else end except exists explain fetch foreign from full function grant group having if ilike in index inner insert intersect into is join key lateral left like limit matched merge natural next not offset on only or order outer over partition primary procedure recursive references replace return returning returns revoke right rollback rows schema select set table then top transaction trigger truncate union unique update using vacuum values view when where window with",
      lit: "null true false",
      types: "bigint bigserial blob bool boolean bytea char date decimal double float int integer interval json jsonb numeric precision real serial smallint text time timestamp timestamptz tinyint uuid varchar",
      ci: true
    });
  },
  css: function () {
    return hlLang([hlRule("com", HL_RE.block), hlRule("str", HL_RE.dq), hlRule("str", HL_RE.sq),
      hlRule(function (text, code, end, ctx) { ctx.depth = text === "{" ? ctx.depth + 1 : Math.max(0, ctx.depth - 1); return null; }, /[{}]/y),
      hlRule("kw", /@[\w-]+/y), hlRule("kw", /!important/y), hlRule("var", /--[\w-]+|\$[\w-]+/y),
      hlRule("num", /#[\da-fA-F]{3,8}(?![\w-])/y, function (c, p, ctx) { return ctx.depth > 0; }),
      hlRule("type", /[.#][A-Za-z_-][\w-]*/y, function (c, p, ctx) { return ctx.depth === 0; }),
      hlRule("kw", /::?[A-Za-z-]+/y, function (c, p, ctx) { return ctx.depth === 0; }),
      hlRule("num", /(?:\d+(?:\.\d+)?|\.\d+)(?:%|[A-Za-z]{1,6})?/y)], {
      classify: function (w, code, end, ctx) {
        if (ctx.depth === 0) return "tag";
        var next = hlNextChar(code, end);
        return next === ":" ? "attr" : next === "(" ? "fn" : null;
      }
    }, /-?[A-Za-z_][\w-]*/y, /[\s(),;\[\]=+*%&|^~?<>]+/y);
  },
  rust: function () {
    return hlLang([hlRule("com", HL_RE.lineSlash), hlRule("com", HL_RE.block), hlRule("str", HL_RE.dq), hlRule("meta", /'[A-Za-z_][A-Za-z0-9_]*(?![\w'])/y),
      hlRule("str", HL_RE.char), hlRule("meta", /#!?\[[^\]\n]*\]?/y), hlRule("num", HL_RE.num)], {
      kw: "as async await break const continue crate dyn else enum extern fn for if impl in let loop macro_rules match mod move mut pub ref return self Self static struct super trait type union unsafe use where while",
      lit: "true false None Some Ok Err",
      types: "i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize f32 f64 bool char str String Vec Option Result Box",
      caps: true, macro: true
    });
  },
  c: function () {
    return hlLang(HL_CLIKE_COMMON.concat([hlRule("str", HL_RE.char), hlRule("meta", /#[ \t]*[A-Za-z]\w*/y, hlLineStart), hlRule("num", HL_RE.num)]), {
      kw: HL_C_KW, lit: "true false NULL nullptr", types: HL_C_TYPES, caps: true
    });
  },
  java: function () {
    return hlLang(HL_CLIKE_COMMON.concat([hlRule("str", HL_RE.char), hlRule("num", HL_RE.num), hlRule("meta", HL_RE.annot)]), {
      kw: HL_JAVA_KW, lit: "true false null", types: HL_JAVA_TYPES, caps: true
    });
  },
  kotlin: function () {
    return hlLang(HL_CLIKE_COMMON.concat([hlRule("str", HL_RE.char), hlRule("num", HL_RE.num), hlRule("meta", HL_RE.annot)]), {
      kw: "abstract actual annotation as break by catch class companion const constructor continue crossinline data do else enum expect external final finally for fun get if import in infix init inline inner interface internal is lateinit noinline object open operator out override package private protected public reified return sealed set super suspend tailrec this throw try typealias val var vararg when where while",
      lit: "true false null", types: "Any Boolean Byte Char Double Float Int Long Nothing Short String Unit", caps: true
    });
  },
  csharp: function () {
    return hlLang(HL_CLIKE_COMMON.concat([hlRule("str", HL_RE.char), hlRule("meta", /#[ \t]*[A-Za-z]\w*/y, hlLineStart), hlRule("num", HL_RE.num)]), {
      kw: "abstract as async await base break case catch checked class const continue default delegate do dynamic else enum event explicit extern finally fixed for foreach get goto if implicit in init interface internal is lock nameof namespace new operator out override params partial private protected public readonly record ref return sealed set sizeof stackalloc static struct switch this throw try typeof unchecked unsafe using var virtual volatile when where while yield",
      lit: "true false null", types: "bool byte char decimal double float int long object sbyte short string uint ulong ushort void", caps: true
    });
  },
  swift: function () {
    return hlLang(HL_CLIKE_COMMON.concat([hlRule("num", HL_RE.num), hlRule("meta", HL_RE.annot)]), {
      kw: "actor any as associatedtype async await break case catch class continue convenience default defer deinit do else enum extension fallthrough fileprivate final for func guard if import in init inout internal is lazy let mutating nonisolated open operator override private protocol public repeat required rethrows return self Self some static struct subscript super switch throw throws try typealias var weak where while",
      lit: "true false nil", types: "Bool Character Double Float Int String UInt Void", caps: true
    });
  },
  clike: function () {
    return hlLang(HL_CLIKE_COMMON.concat([hlRule("str", HL_RE.sq), hlRule("str", HL_RE.tpl), hlRule("num", HL_RE.num), hlRule("meta", HL_RE.annot)]), {
      kw: HL_C_KW + " " + HL_JAVA_KW + " fn func fun let val def match impl trait pub mod use echo elseif foreach",
      lit: "true false null nil NULL nullptr", types: HL_C_TYPES + " " + HL_JAVA_TYPES, caps: true
    });
  }
};

var HL_ALIASES = (function () {
  var map = Object.create(null), groups = {
    go: "go golang",
    js: "js javascript jsx mjs cjs node ts typescript tsx mts cts deno",
    json: "json jsonc json5 geojson webmanifest",
    python: "py python python3 py3 pyw ipython",
    shell: "sh bash shell zsh ksh fish console shell-session shellscript terminal",
    powershell: "ps ps1 psm1 psd1 powershell pwsh posh",
    diff: "diff patch udiff",
    markup: "html htm xhtml xml svg xsl xslt plist vue svelte rss atom",
    css: "css scss less sass postcss",
    sql: "sql mysql postgres postgresql psql pgsql sqlite plsql tsql mssql mariadb",
    rust: "rust rs",
    c: "c h cpp c++ cc cxx hpp hh hxx ino arduino cuda cu objc objective-c objectivec glsl hlsl metal",
    java: "java",
    kotlin: "kotlin kt kts",
    csharp: "csharp cs c# dotnet",
    swift: "swift",
    clike: "scala dart groovy gradle php zig d solidity sol proto protobuf"
  };
  Object.keys(groups).forEach(function (name) { groups[name].split(" ").forEach(function (alias) { map[alias] = name; }); });
  return map;
})();
var hlSpecCache = Object.create(null);
function hlSpec(name) { return hlSpecCache[name] || (hlSpecCache[name] = HL_BUILD[name]()); }

function hlLangName(lang) {
  var m = /^[\s{.]*([A-Za-z0-9_#+-]{1,40})/.exec(String(lang || ""));
  if (!m) return "";
  var name = m[1].toLowerCase();
  if (name.indexOf("language-") === 0) name = name.slice(9);
  return HL_ALIASES[name] || "";
}

function hlDiff(code) {
  var inHunk = false;
  return code.split("\n").map(function (line) {
    var c = line.charAt(0), cls = null;
    if (/^(?:diff |index |similarity |rename |new file|deleted file|old mode|new mode)/.test(line)) { inHunk = false; cls = "meta"; }
    else if (line.lastIndexOf("@@", 0) === 0) { inHunk = true; cls = "hunk"; }
    else if (!inHunk && /^(?:\+\+\+|---)(?: |$)/.test(line)) cls = "meta";
    else if (c === "+") cls = "add";
    else if (c === "-") cls = "del";
    else if (c === "\\") cls = "com";
    return cls ? hlSpan(cls, line) : escHtml(line);
  }).join("\n");
}

var HL_MARKUP_TOKEN = /<!--[\s\S]*?(?:-->|$)|<!\[CDATA\[[\s\S]*?(?:\]\]>|$)|<[!?][^>]*>?|<\/?[A-Za-z][\w:.-]*|&#?\w{1,32};/g;
var HL_MARKUP_ATTR = /\s+|\/?>|"[^"]*"?|'[^']*'?|=|[^\s"'=<>\/]+|\//y;
var HL_MARKUP_END = {script: /<\/script/gi, style: /<\/style/gi};

function hlMarkup(code) {
  var parts = [], last = 0, re = HL_MARKUP_TOKEN, m;
  re.lastIndex = 0;
  while ((m = re.exec(code)) !== null) {
    var tok = m[0], pos = m.index + tok.length;
    if (m.index > last) parts.push(escHtml(code.slice(last, m.index)));
    if (tok.charAt(0) === "&") parts.push(hlSpan("num", tok));
    else if (tok.lastIndexOf("<!--", 0) === 0) parts.push(hlSpan("com", tok));
    else if (tok.charAt(1) === "!" || tok.charAt(1) === "?") parts.push(hlSpan("meta", tok));
    else {
      var close = tok.charAt(1) === "/", name = tok.slice(close ? 2 : 1), afterEq = false, end = "";
      parts.push(escHtml(close ? "</" : "<") + hlSpan("tag", name));
      while (pos < code.length) {
        HL_MARKUP_ATTR.lastIndex = pos;
        var a = HL_MARKUP_ATTR.exec(code);
        if (!a) break;
        var s = a[0], ch = s.charAt(0);
        pos += s.length;
        if (s === ">" || s === "/>") { parts.push(escHtml(s)); end = s; break; }
        if (ch === '"' || ch === "'") { parts.push(hlSpan("str", s)); afterEq = false; }
        else if (s === "=") { parts.push("="); afterEq = true; }
        else if (s === "/" || !s.trim()) parts.push(escHtml(s));
        else { parts.push(hlSpan(afterEq ? "str" : "attr", s)); afterEq = false; }
      }
      var endRe = !close && end === ">" && HL_MARKUP_END[name.toLowerCase()];
      if (endRe) {
        endRe.lastIndex = pos;
        var found = endRe.exec(code), stop = found ? found.index : code.length;
        if (stop > pos) parts.push(hlRun(code.slice(pos, stop), hlSpec(name.toLowerCase() === "style" ? "css" : "js")));
        pos = stop;
      }
    }
    last = pos;
    re.lastIndex = pos;
  }
  if (last < code.length) parts.push(escHtml(code.slice(last)));
  return parts.join("");
}

// Returns highlighted, fully escaped HTML for a fenced block, or null when the
// language is unknown/unsupported or the block is too large (caller escapes).
function highlightCodeHTML(code, lang) {
  code = String(code == null ? "" : code);
  var name = hlLangName(lang);
  if (!name || !code || code.length > HL_MAX_CHARS) return null;
  var key = code.length >= HL_CACHE_MIN ? name + "\u0000" + code : null;
  if (key !== null && hlCache.has(key)) {
    var hit = hlCache.get(key);
    hlCache.delete(key); hlCache.set(key, hit);
    return hit;
  }
  var html;
  try {
    html = name === "diff" ? hlDiff(code) : name === "markup" ? hlMarkup(code) : hlRun(code, hlSpec(name));
  } catch (error) { return null; }
  if (key !== null) {
    hlCache.set(key, html);
    hlCacheChars += key.length + html.length;
    while (hlCache.size > HL_CACHE_MAX || hlCacheChars > HL_CACHE_CHARS) {
      var oldest = hlCache.keys().next().value;
      hlCacheChars -= oldest.length + hlCache.get(oldest).length;
      hlCache.delete(oldest);
    }
  }
  return html;
}
