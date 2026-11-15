"""Long-context Python debugging: a report test fails with wrong totals; the cause is a column-letter
conversion deep inside a ~1300-line spreadsheet engine that only breaks past column Z."""
import math

from common import Task, d, cmd, UNITTEST

ENGINE_HEAD = '''
"""sheetcalc engine: a small spreadsheet formula evaluator.

Cells are addressed like A1 ... ZZZ9999999 (columns are bijective base-26
letters: A..Z, then AA..AZ, BA..BZ, ..., ZZ, then AAA). Internally a cell is
a 0-based (row, col) pair.

Formula grammar (a cell whose text starts with "="):

    expr     := compare
    compare  := concat (("=" | "<>" | "<" | ">" | "<=" | ">=") concat)*
    concat   := additive ("&" additive)*
    additive := term (("+" | "-") term)*
    term     := power (("*" | "/") power)*
    power    := unary ("^" power)?          (right associative)
    unary    := "-" unary | "+" unary | postfix
    postfix  := atom "%"*
    atom     := NUMBER | STRING | TRUE | FALSE | REF | REF ":" REF
              | NAME "(" [expr ("," expr)*] ")" | "(" expr ")"

Ranges are only valid as function arguments; they evaluate to the list of
their cell values in row-major order.
"""

import math
import re


class SheetError(Exception):
    """Base class of all engine errors."""


class FormulaError(SheetError):
    """A formula could not be parsed or evaluated."""


class CycleError(SheetError):
    """A formula depends on itself."""


# --------------------------------------------------------------------- cell names

_REF_RE = re.compile(r"^\\$?([A-Za-z]{1,3})\\$?([0-9]{1,7})$")


def col_to_index(letters):
    """Column letters to a 0-based index: A -> 0, Z -> 25, AA -> 26, AZ -> 51, BA -> 52."""
    n = 0
    for ch in letters.upper():
        n = n * 26 + (ord(ch) - ord("A"))
    return n


def index_to_col(index):
    """0-based column index to letters: 0 -> A, 25 -> Z, 26 -> AA."""
    if index < 0:
        raise ValueError("negative column index")
    s = ""
    index += 1
    while index:
        index, r = divmod(index - 1, 26)
        s = chr(ord("A") + r) + s
    return s


def parse_ref(name):
    """"B12" -> (11, 1). Dollar signs (absolute references) are ignored."""
    m = _REF_RE.match(name.strip())
    if not m:
        raise FormulaError("bad cell reference: %r" % name)
    row = int(m.group(2))
    if row < 1:
        raise FormulaError("bad cell reference: %r" % name)
    return row - 1, col_to_index(m.group(1))


def cell_name(row, col):
    """(11, 1) -> "B12"."""
    return "%s%d" % (index_to_col(col), row + 1)


def range_cells(r1, c1, r2, c2):
    """All (row, col) pairs of a rectangle, row-major, corners in any order."""
    top, bottom = min(r1, r2), max(r1, r2)
    left, right = min(c1, c2), max(c1, c2)
    return [(r, c) for r in range(top, bottom + 1) for c in range(left, right + 1)]


# --------------------------------------------------------------------- tokenizer

_TOKEN_RE = re.compile(r"""
    (?P<ws>\\s+)
  | (?P<num>(?:\\d+(?:\\.\\d*)?|\\.\\d+)(?:[eE][+-]?\\d+)?)
  | (?P<str>"(?:[^"]|"")*")
  | (?P<ref>\\$?[A-Za-z]{1,3}\\$?\\d+)(?![A-Za-z0-9_(.])
  | (?P<name>[A-Za-z_][A-Za-z0-9_.]*)
  | (?P<op><>|<=|>=|[-+*/^&=<>%:,()])
""", re.X)


class Token:
    __slots__ = ("kind", "text", "pos")

    def __init__(self, kind, text, pos):
        self.kind, self.text, self.pos = kind, text, pos

    def __repr__(self):
        return "Token(%s, %r, %d)" % (self.kind, self.text, self.pos)


def tokenize(src):
    """Split formula text (without the leading "=") into tokens."""
    out, pos = [], 0
    while pos < len(src):
        m = _TOKEN_RE.match(src, pos)
        if not m:
            raise FormulaError("unexpected character %r at %d" % (src[pos], pos))
        kind = m.lastgroup
        if kind != "ws":
            out.append(Token(kind, m.group(kind), pos))
        pos = m.end()
    out.append(Token("eof", "", pos))
    return out


# --------------------------------------------------------------------- parser

_COMPARE = {"=", "<>", "<", ">", "<=", ">="}


class Parser:
    """Recursive-descent parser producing tuples:

    ("num", float) ("str", s) ("bool", b) ("ref", row, col)
    ("range", r1, c1, r2, c2) ("call", NAME, [args]) ("bin", op, a, b)
    ("neg", a) ("pct", a)
    """

    def __init__(self, src):
        self.src = src
        self.toks = tokenize(src)
        self.i = 0

    def peek(self):
        return self.toks[self.i]

    def take(self):
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def expect(self, text):
        tok = self.take()
        if tok.text != text:
            raise FormulaError("expected %r at %d in %r" % (text, tok.pos, self.src))
        return tok

    def parse(self):
        node = self.compare()
        if self.peek().kind != "eof":
            raise FormulaError("unexpected %r at %d in %r" % (self.peek().text, self.peek().pos, self.src))
        return node

    def compare(self):
        node = self.concat()
        while self.peek().kind == "op" and self.peek().text in _COMPARE:
            op = self.take().text
            node = ("bin", op, node, self.concat())
        return node

    def concat(self):
        node = self.additive()
        while self.peek().text == "&":
            self.take()
            node = ("bin", "&", node, self.additive())
        return node

    def additive(self):
        node = self.term()
        while self.peek().kind == "op" and self.peek().text in ("+", "-"):
            op = self.take().text
            node = ("bin", op, node, self.term())
        return node

    def term(self):
        node = self.power()
        while self.peek().kind == "op" and self.peek().text in ("*", "/"):
            op = self.take().text
            node = ("bin", op, node, self.power())
        return node

    def power(self):
        node = self.unary()
        if self.peek().text == "^":
            self.take()
            return ("bin", "^", node, self.power())
        return node

    def unary(self):
        if self.peek().kind == "op" and self.peek().text == "-":
            self.take()
            return ("neg", self.unary())
        if self.peek().kind == "op" and self.peek().text == "+":
            self.take()
            return self.unary()
        return self.postfix()

    def postfix(self):
        node = self.atom()
        while self.peek().text == "%":
            self.take()
            node = ("pct", node)
        return node

    def atom(self):
        tok = self.take()
        if tok.kind == "num":
            return ("num", float(tok.text))
        if tok.kind == "str":
            return ("str", tok.text[1:-1].replace('""', '"'))
        if tok.kind == "ref":
            r, c = parse_ref(tok.text)
            if self.peek().text == ":":
                self.take()
                end = self.take()
                if end.kind != "ref":
                    raise FormulaError("bad range end at %d in %r" % (end.pos, self.src))
                r2, c2 = parse_ref(end.text)
                return ("range", r, c, r2, c2)
            return ("ref", r, c)
        if tok.kind == "name":
            upper = tok.text.upper()
            if self.peek().text == "(":
                self.take()
                args = []
                if self.peek().text != ")":
                    args.append(self.compare())
                    while self.peek().text == ",":
                        self.take()
                        args.append(self.compare())
                self.expect(")")
                return ("call", upper, args)
            if upper in ("TRUE", "FALSE"):
                return ("bool", upper == "TRUE")
            raise FormulaError("unknown name %r at %d" % (tok.text, tok.pos))
        if tok.text == "(":
            node = self.compare()
            self.expect(")")
            return node
        raise FormulaError("unexpected %r at %d in %r" % (tok.text or "end", tok.pos, self.src))


def parse_formula(text):
    """Parse formula text; a leading "=" is optional."""
    if text.startswith("="):
        text = text[1:]
    return Parser(text).parse()


# --------------------------------------------------------------------- values

def to_number(v):
    """Coerce a scalar to float: empty -> 0, bool -> 0/1, numeric text -> value."""
    if v is None:
        return 0.0
    if isinstance(v, bool):
        return 1.0 if v else 0.0
    if isinstance(v, (int, float)):
        return float(v)
    if isinstance(v, str):
        try:
            return float(v.strip()) if v.strip() else 0.0
        except ValueError:
            raise FormulaError("not a number: %r" % v)
    raise FormulaError("not a scalar: %r" % (v,))


def to_text(v):
    if v is None:
        return ""
    if isinstance(v, bool):
        return "TRUE" if v else "FALSE"
    if isinstance(v, float):
        return ("%d" % v) if v.is_integer() else repr(v)
    return str(v)


def to_bool(v):
    if isinstance(v, str):
        if v.upper() in ("TRUE", "FALSE"):
            return v.upper() == "TRUE"
        raise FormulaError("not a boolean: %r" % v)
    return bool(to_number(v))


def flatten(args):
    """Flatten range arguments (lists) into one list of scalars."""
    out = []
    for a in args:
        if isinstance(a, list):
            out.extend(a)
        else:
            out.append(a)
    return out


def numbers(args):
    """Numbers among args. Inside ranges, text, booleans and empty cells are
    skipped; a direct argument is coerced with to_number."""
    out = []
    for a in args:
        if isinstance(a, list):
            out.extend(float(x) for x in a if isinstance(x, (int, float)) and not isinstance(x, bool))
        else:
            out.append(to_number(a))
    return out


def scalar(v, what="argument"):
    if isinstance(v, list):
        raise FormulaError("%s must be a single value, not a range" % what)
    return v
'''

ENGINE_CORE_FUNCS = '''

# --------------------------------------------------------------------- core functions

def fn_sum(args):
    return sum(numbers(args))


def fn_average(args):
    ns = numbers(args)
    if not ns:
        raise FormulaError("AVERAGE of nothing")
    return sum(ns) / len(ns)


def fn_min(args):
    ns = numbers(args)
    return min(ns) if ns else 0.0


def fn_max(args):
    ns = numbers(args)
    return max(ns) if ns else 0.0


def fn_count(args):
    return float(len(numbers([a if isinstance(a, list) else [a] for a in args])))


def fn_counta(args):
    return float(sum(1 for v in flatten(args) if v not in (None, "")))


def fn_round(args):
    if len(args) not in (1, 2):
        raise FormulaError("ROUND takes 1 or 2 arguments")
    x = to_number(scalar(args[0]))
    digits = int(to_number(scalar(args[1]))) if len(args) == 2 else 0
    q = 10.0 ** digits
    return math.floor(abs(x) * q + 0.5) / q * (1 if x >= 0 else -1)


def fn_abs(args):
    return abs(to_number(scalar(args[0])))


def fn_concat(args):
    return "".join(to_text(v) for v in flatten(args))


def fn_len(args):
    return float(len(to_text(scalar(args[0]))))


def fn_upper(args):
    return to_text(scalar(args[0])).upper()


def fn_lower(args):
    return to_text(scalar(args[0])).lower()


def fn_and(args):
    return all(to_bool(v) for v in flatten(args))


def fn_or(args):
    return any(to_bool(v) for v in flatten(args))


def fn_not(args):
    return not to_bool(scalar(args[0]))


def fn_match(args):
    """MATCH(value, range, 0): 1-based position of the first exact match."""
    if len(args) != 3 or to_number(scalar(args[2])) != 0:
        raise FormulaError("only exact MATCH(value, range, 0) is supported")
    needle, hay = scalar(args[0]), args[1]
    if not isinstance(hay, list):
        raise FormulaError("MATCH needs a range")
    for i, v in enumerate(hay):
        if v == needle or (not isinstance(v, str) and not isinstance(needle, str) and v is not None
                           and to_number(v) == to_number(needle)):
            return float(i + 1)
    raise FormulaError("MATCH: value not found")


def fn_index(args):
    """INDEX(range, n): the n-th value (1-based) of a range."""
    hay, n = args[0], int(to_number(scalar(args[1])))
    if not isinstance(hay, list) or not 1 <= n <= len(hay):
        raise FormulaError("INDEX out of range")
    return hay[n - 1]


def fn_sumif(args):
    """SUMIF(range, value[, sum_range])."""
    crit = scalar(args[1])
    rng = args[0]
    vals = args[2] if len(args) > 2 else rng
    if not isinstance(rng, list) or not isinstance(vals, list) or len(rng) != len(vals):
        raise FormulaError("SUMIF needs two ranges of equal size")
    return sum(to_number(v) for k, v in zip(rng, vals) if k == crit and isinstance(v, (int, float)))


def fn_countif(args):
    crit = scalar(args[1])
    if not isinstance(args[0], list):
        raise FormulaError("COUNTIF needs a range")
    return float(sum(1 for v in args[0] if v == crit))
'''

ENGINE_TAIL = '''

# --------------------------------------------------------------------- sheet

class Sheet:
    """A grid of cells. Plain values are stored as given; text starting with
    "=" is a formula, evaluated lazily and cached until the next change."""

    def __init__(self):
        self.cells = {}
        self._cache = {}
        self._active = set()

    # -- writing
    def set(self, name, value):
        self.set_rc(*parse_ref(name), value)

    def set_rc(self, row, col, value):
        if isinstance(value, str) and value.startswith("="):
            value = ("formula", value, parse_formula(value))
        if value is None or value == "":
            self.cells.pop((row, col), None)
        else:
            self.cells[(row, col)] = value
        self._cache.clear()

    # -- reading
    def raw(self, name):
        v = self.cells.get(parse_ref(name))
        return v[1] if isinstance(v, tuple) else v

    def get(self, name):
        return self.value(*parse_ref(name))

    def value(self, row, col):
        key = (row, col)
        if key in self._cache:
            return self._cache[key]
        v = self.cells.get(key)
        if isinstance(v, tuple):
            if key in self._active:
                raise CycleError("cycle through %s" % cell_name(row, col))
            self._active.add(key)
            try:
                v = self.eval(v[2])
            finally:
                self._active.discard(key)
        if isinstance(v, list):
            raise FormulaError("%s: a range is not a cell value" % cell_name(row, col))
        self._cache[key] = v
        return v

    def bounds(self):
        """(rows, cols) of the used area."""
        if not self.cells:
            return 0, 0
        return max(r for r, _ in self.cells) + 1, max(c for _, c in self.cells) + 1

    # -- evaluation
    def eval(self, node):
        kind = node[0]
        if kind in ("num", "str", "bool"):
            return node[1]
        if kind == "ref":
            return self.value(node[1], node[2])
        if kind == "range":
            return [self.value(r, c) for r, c in range_cells(*node[1:])]
        if kind == "neg":
            return -to_number(scalar(self.eval(node[1])))
        if kind == "pct":
            return to_number(scalar(self.eval(node[1]))) / 100.0
        if kind == "call":
            return self.call(node[1], node[2])
        if kind == "bin":
            return self.binary(node[1], node[2], node[3])
        raise FormulaError("bad node %r" % (kind,))

    def call(self, name, arg_nodes):
        if name == "IF":
            if len(arg_nodes) not in (2, 3):
                raise FormulaError("IF takes 2 or 3 arguments")
            if to_bool(scalar(self.eval(arg_nodes[0]))):
                return self.eval(arg_nodes[1])
            return self.eval(arg_nodes[2]) if len(arg_nodes) == 3 else False
        if name == "IFERROR":
            try:
                return self.eval(arg_nodes[0])
            except FormulaError:
                return self.eval(arg_nodes[1])
        fn = FUNCTIONS.get(name)
        if fn is None:
            raise FormulaError("unknown function %s" % name)
        return fn([self.eval(a) for a in arg_nodes])

    def binary(self, op, left, right):
        a, b = scalar(self.eval(left)), scalar(self.eval(right))
        if op == "&":
            return to_text(a) + to_text(b)
        if op in _COMPARE:
            if isinstance(a, str) or isinstance(b, str):
                x, y = to_text(a).lower(), to_text(b).lower()
            else:
                x, y = to_number(a), to_number(b)
            return {"=": x == y, "<>": x != y, "<": x < y, ">": x > y, "<=": x <= y, ">=": x >= y}[op]
        x, y = to_number(a), to_number(b)
        if op == "+":
            return x + y
        if op == "-":
            return x - y
        if op == "*":
            return x * y
        if op == "/":
            if y == 0:
                raise FormulaError("division by zero")
            return x / y
        if op == "^":
            return x ** y
        raise FormulaError("bad operator %r" % op)


def evaluate(sheet, formula):
    """Evaluate a formula against sheet without storing it."""
    return sheet.eval(parse_formula(formula))
'''

# Unit conversions and finance/stat helpers pad the engine to a realistic size.
CONVERSIONS = [
    ("KM2MI", "kilometres to miles", "x / 1.609344"), ("MI2KM", "miles to kilometres", "x * 1.609344"),
    ("KG2LB", "kilograms to pounds", "x / 0.45359237"), ("LB2KG", "pounds to kilograms", "x * 0.45359237"),
    ("C2F", "Celsius to Fahrenheit", "x * 9 / 5 + 32"), ("F2C", "Fahrenheit to Celsius", "(x - 32) * 5 / 9"),
    ("L2GAL", "litres to US gallons", "x / 3.785411784"), ("GAL2L", "US gallons to litres", "x * 3.785411784"),
    ("M2FT", "metres to feet", "x / 0.3048"), ("FT2M", "feet to metres", "x * 0.3048"),
    ("CM2IN", "centimetres to inches", "x / 2.54"), ("IN2CM", "inches to centimetres", "x * 2.54"),
    ("KWH2MJ", "kilowatt-hours to megajoules", "x * 3.6"), ("MJ2KWH", "megajoules to kilowatt-hours", "x / 3.6"),
    ("HA2AC", "hectares to acres", "x / 0.40468564224"), ("AC2HA", "acres to hectares", "x * 0.40468564224"),
    ("KMH2MS", "km/h to m/s", "x / 3.6"), ("MS2KMH", "m/s to km/h", "x * 3.6"),
    ("KN2KMH", "knots to km/h", "x * 1.852"), ("KMH2KN", "km/h to knots", "x / 1.852"),
    ("BAR2PSI", "bar to psi", "x * 14.503773773"), ("PSI2BAR", "psi to bar", "x / 14.503773773"),
    ("DEG2RAD", "degrees to radians", "x * math.pi / 180"), ("RAD2DEG", "radians to degrees", "x * 180 / math.pi"),
    ("H2MIN", "hours to minutes", "x * 60"), ("MIN2H", "minutes to hours", "x / 60"),
    ("D2H", "days to hours", "x * 24"), ("H2D", "hours to days", "x / 24"),
    ("KB2B", "kibibytes to bytes", "x * 1024"), ("B2KB", "bytes to kibibytes", "x / 1024"),
]

MATH1 = [
    ("SQRT", "square root", "math.sqrt(x) if x >= 0 else _fail('SQRT of a negative number')"),
    ("EXP", "e to the power x", "math.exp(x)"), ("LN", "natural logarithm", "math.log(x) if x > 0 else _fail('LN domain')"),
    ("LOG10", "base-10 logarithm", "math.log10(x) if x > 0 else _fail('LOG10 domain')"),
    ("SIGN", "sign of x (-1, 0 or 1)", "(x > 0) - (x < 0)"), ("INT", "round down to an integer", "math.floor(x)"),
    ("TRUNC", "truncate toward zero", "math.trunc(x)"), ("CEILING", "round up to an integer", "math.ceil(x)"),
    ("FLOOR", "round down to an integer", "math.floor(x)"), ("EVEN", "round away from zero to an even integer",
                                                             "math.copysign(math.ceil(abs(x) / 2) * 2, x)"),
    ("ODD", "round away from zero to an odd integer", "math.copysign(math.ceil((abs(x) - 1) / 2) * 2 + 1, x)"),
    ("SQUARE", "x squared", "x * x"), ("CUBE", "x cubed", "x * x * x"), ("RECIP", "1 / x", "1 / x if x else _fail('RECIP of zero')"),
    ("SIN", "sine (radians)", "math.sin(x)"), ("COS", "cosine (radians)", "math.cos(x)"), ("TAN", "tangent (radians)", "math.tan(x)"),
    ("FACT", "factorial", "float(math.factorial(int(x))) if x >= 0 else _fail('FACT domain')"),
]

AGG = [
    ("PRODUCT", "product of the numbers", "math.prod(ns) if ns else 0.0"),
    ("SUMSQ", "sum of squares", "sum(n * n for n in ns)"),
    ("MEDIAN", "median", "_median(ns)"),
    ("VARP", "population variance", "_var(ns, 0)"), ("VAR", "sample variance", "_var(ns, 1)"),
    ("STDEVP", "population standard deviation", "math.sqrt(_var(ns, 0))"),
    ("STDEV", "sample standard deviation", "math.sqrt(_var(ns, 1))"),
    ("RANGE", "max minus min", "(max(ns) - min(ns)) if ns else 0.0"),
    ("GEOMEAN", "geometric mean", "math.exp(sum(math.log(n) for n in ns) / len(ns)) if ns and min(ns) > 0 else _fail('GEOMEAN domain')"),
    ("HARMEAN", "harmonic mean", "len(ns) / sum(1 / n for n in ns) if ns and min(ns) > 0 else _fail('HARMEAN domain')"),
    ("AVEDEV", "mean absolute deviation", "sum(abs(n - sum(ns) / len(ns)) for n in ns) / len(ns) if ns else 0.0"),
    ("COUNTPOS", "count of positive numbers", "float(sum(1 for n in ns if n > 0))"),
    ("COUNTNEG", "count of negative numbers", "float(sum(1 for n in ns if n < 0))"),
    ("SUMPOS", "sum of positive numbers", "sum(n for n in ns if n > 0)"),
    ("SUMNEG", "sum of negative numbers", "sum(n for n in ns if n < 0)"),
]

TEXT1 = [
    ("TRIM", "strip surrounding spaces and collapse inner runs", "' '.join(s.split())"),
    ("PROPER", "capitalise every word", "s.title()"), ("REVERSE", "reverse the text", "s[::-1]"),
    ("CLEAN", "drop non-printable characters", "''.join(ch for ch in s if ch.isprintable())"),
    ("INITIALS", "first letter of every word", "''.join(w[0].upper() for w in s.split())"),
    ("SLUG", "lower-case words joined by dashes", "'-'.join(re.findall('[a-z0-9]+', s.lower()))"),
    ("WORDS", "number of words", "float(len(s.split()))"), ("VOWELS", "number of vowels", "float(sum(1 for ch in s.lower() if ch in 'aeiou'))"),
]


def _gen_padding():
    out = ['''

# --------------------------------------------------------------------- generated helpers

def _fail(msg):
    raise FormulaError(msg)


def _median(ns):
    if not ns:
        raise FormulaError("MEDIAN of nothing")
    s = sorted(ns)
    mid = len(s) // 2
    return s[mid] if len(s) % 2 else (s[mid - 1] + s[mid]) / 2


def _var(ns, ddof):
    if len(ns) <= ddof:
        raise FormulaError("not enough values for a variance")
    mean = sum(ns) / len(ns)
    return sum((n - mean) ** 2 for n in ns) / (len(ns) - ddof)
''']
    for name, doc, expr in CONVERSIONS + MATH1:
        out.append('''

def fn_%(lname)s(args):
    """%(name)s(x): %(doc)s."""
    if len(args) != 1:
        raise FormulaError("%(name)s takes exactly one argument")
    x = to_number(scalar(args[0], "%(name)s argument"))
    return float(%(expr)s)
''' % {"lname": name.lower(), "name": name, "doc": doc, "expr": expr})
    for name, doc, expr in AGG:
        out.append('''

def fn_%(lname)s(args):
    """%(name)s(values...): %(doc)s. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(%(expr)s)
''' % {"lname": name.lower(), "name": name, "doc": doc, "expr": expr})
    for name, doc, expr in TEXT1:
        out.append('''

def fn_%(lname)s(args):
    """%(name)s(text): %(doc)s."""
    if len(args) != 1:
        raise FormulaError("%(name)s takes exactly one argument")
    s = to_text(scalar(args[0], "%(name)s argument"))
    return %(expr)s
''' % {"lname": name.lower(), "name": name, "doc": doc, "expr": expr})
    core = ["SUM", "AVERAGE", "MIN", "MAX", "COUNT", "COUNTA", "ROUND", "ABS", "CONCAT", "LEN", "UPPER", "LOWER",
            "AND", "OR", "NOT", "MATCH", "INDEX", "SUMIF", "COUNTIF"]
    names = core + [n for n, _, _ in CONVERSIONS + MATH1 + AGG + TEXT1]
    out.append("\n\nFUNCTIONS = {\n" + "".join('    "%s": fn_%s,\n' % (n, n.lower()) for n in names) + "}\n")
    return "".join(out)


def build():
    t = Task("py-sheet-columns")
    engine = ENGINE_HEAD.lstrip("\n") + ENGINE_CORE_FUNCS + _gen_padding() + ENGINE_TAIL
    t.fx("sheetcalc/__init__.py", d('''
        """sheetcalc: spreadsheet formulas for CSV reports."""

        from sheetcalc.engine import FormulaError, Sheet, cell_name, evaluate, parse_ref

        __all__ = ["FormulaError", "Sheet", "cell_name", "evaluate", "parse_ref"]
        '''))
    t.fx("sheetcalc/engine.py", engine)
    t.fx("sheetcalc/csvio.py", d('''
        """Load CSV files into sheets."""

        import csv

        from sheetcalc.engine import Sheet


        def _cell(text):
            text = text.strip()
            if text.startswith("="):
                return text
            try:
                return float(text)
            except ValueError:
                return text


        def load_csv(path):
            """Sheet with the CSV contents; numeric text becomes numbers."""
            sheet = Sheet()
            with open(path, newline="", encoding="utf-8") as f:
                for r, row in enumerate(csv.reader(f)):
                    for c, text in enumerate(row):
                        if text.strip():
                            sheet.set_rc(r, c, _cell(text))
            return sheet
        '''))
    t.fx("sheetcalc/report.py", d('''
        """Monthly station report: per-station totals computed with sheet formulas.

        Input CSV: header row ("station", day 1, day 2, ...), then one row per
        station with that day's ride count in every following column.
        """

        from sheetcalc.csvio import load_csv
        from sheetcalc.engine import cell_name


        def summarize(path):
            """{station: (total, average rounded to 2 places, best day)} plus the grand total.

            The best day is the first day with the highest count (1-based).
            """
            sheet = load_csv(path)
            rows, cols = sheet.bounds()
            first, last = cell_name(0, 1)[:-1], cell_name(0, cols - 1)[:-1]
            out = {}
            for r in range(1, rows):
                span = "%s%d:%s%d" % (first, r + 1, last, r + 1)
                sheet.set_rc(r, cols, "=SUM(%s)" % span)
                sheet.set_rc(r, cols + 1, "=ROUND(AVERAGE(%s), 2)" % span)
                sheet.set_rc(r, cols + 2, "=MATCH(MAX(%s), %s, 0)" % (span, span))
            total_col = cell_name(0, cols)[:-1]
            sheet.set_rc(rows, cols, "=SUM(%s2:%s%d)" % (total_col, total_col, rows))
            for r in range(1, rows):
                station = sheet.value(r, 0)
                out[station] = (int(sheet.value(r, cols)), sheet.value(r, cols + 1), int(sheet.value(r, cols + 2)))
            return out, int(sheet.value(rows, cols))


        def render(path):
            stations, total = summarize(path)
            lines = ["%-12s %7s %8s %4s" % ("station", "total", "avg", "best")]
            for name in sorted(stations):
                tot, avg, best = stations[name]
                lines.append("%-12s %7d %8.2f %4d" % (name, tot, avg, best))
            lines.append("%-12s %7d" % ("ALL", total))
            return "\\n".join(lines) + "\\n"
        '''))

    rng = t.rng
    stations = ["Rynek", "Dworzec", "Kampus", "Port", "Arena", "Ogrod", "Stocznia", "Lotnisko", "Zamek", "Most",
                "Park", "Targ"]
    days = 30
    data = {}
    for s in stations:
        counts = [rng.randint(20, 400) for _ in range(days)]
        # make the busiest day fall after column Z (days 25-30 sit in columns Z..AE) for most stations
        if rng.random() < 0.75:
            counts[rng.randint(25, days - 1)] = 400 + rng.randint(1, 120)
        data[s] = counts
    csv_text = "station," + ",".join(str(i + 1) for i in range(days)) + "\n"
    csv_text += "".join(s + "," + ",".join(map(str, data[s])) + "\n" for s in stations)
    t.fx("data/june.csv", csv_text)

    def expect(counts):
        avg = sum(counts) / len(counts)
        avg = math.floor(avg * 100 + 0.5) / 100
        return sum(counts), avg, counts.index(max(counts)) + 1

    exp = {s: expect(c) for s, c in data.items()}
    grand = sum(sum(c) for c in data.values())
    pick = [stations[0], stations[5], stations[9]]
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_engine.py", d('''
        import unittest

        from sheetcalc import FormulaError, Sheet, cell_name, evaluate, parse_ref


        class EngineTest(unittest.TestCase):
            def test_refs(self):
                self.assertEqual(parse_ref("B12"), (11, 1))
                self.assertEqual(parse_ref("$C$3"), (2, 2))
                self.assertEqual(cell_name(0, 0), "A1")
                self.assertEqual(cell_name(9, 25), "Z10")

            def test_arithmetic(self):
                s = Sheet()
                self.assertEqual(evaluate(s, "=1+2*3"), 7)
                self.assertEqual(evaluate(s, "=2^3^2"), 512)
                self.assertEqual(evaluate(s, "=-2^2"), 4)
                self.assertEqual(evaluate(s, "=50%"), 0.5)
                self.assertEqual(evaluate(s, '="a"&1'), "a1")

            def test_cells_and_ranges(self):
                s = Sheet()
                for i, v in enumerate([3, 1, 4, 1, 5]):
                    s.set("B%d" % (i + 1), v)
                s.set("C1", "=SUM(B1:B5)")
                s.set("C2", "=MATCH(MAX(B1:B5), B1:B5, 0)")
                s.set("C3", "=IF(C1>10, \\"big\\", \\"small\\")")
                self.assertEqual(s.get("C1"), 14)
                self.assertEqual(s.get("C2"), 5)
                self.assertEqual(s.get("C3"), "big")
                s.set("B5", 0)
                self.assertEqual(s.get("C1"), 9)

            def test_functions(self):
                s = Sheet()
                self.assertEqual(evaluate(s, "=ROUND(2.375, 2)"), 2.38)
                self.assertEqual(evaluate(s, "=MEDIAN(1, 9, 3)"), 3)
                self.assertAlmostEqual(evaluate(s, "=KM2MI(1.609344)"), 1.0)
                self.assertEqual(evaluate(s, '=TRIM("  a   b ")'), "a b")

            def test_errors(self):
                s = Sheet()
                with self.assertRaises(FormulaError):
                    evaluate(s, "=1/0")
                with self.assertRaises(FormulaError):
                    evaluate(s, "=NOPE(1)")
                s.set("A1", "=A2")
                s.set("A2", "=A1")
                with self.assertRaises(Exception):
                    s.get("A1")


        if __name__ == "__main__":
            unittest.main()
        '''))
    t.fx("tests/test_report.py", d('''
        import os
        import unittest

        from sheetcalc import report

        DATA = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "june.csv")


        class ReportTest(unittest.TestCase):
            def test_june(self):
                stations, total = report.summarize(DATA)
                self.assertEqual(len(stations), %(n)d)
                want = %(want)r
                for name, expected in want.items():
                    self.assertEqual(stations[name], expected, name)
                self.assertEqual(total, %(grand)d)

            def test_render(self):
                text = report.render(DATA)
                self.assertTrue(text.startswith("station"))
                self.assertIn("ALL", text)


        if __name__ == "__main__":
            unittest.main()
        ''') % {"n": len(stations), "want": {s: exp[s] for s in pick}, "grand": grand})
    t.fx("README.md", d('''
        # sheetcalc

        Spreadsheet-style formulas (sheetcalc/engine.py) used to build monthly CSV
        reports (sheetcalc/report.py). Tests: `python -m unittest discover -s tests -t .`.
        '''))

    fixed = engine.replace('''    n = 0
    for ch in letters.upper():
        n = n * 26 + (ord(ch) - ord("A"))
    return n''', '''    n = 0
    for ch in letters.upper():
        n = n * 26 + (ord(ch) - ord("A") + 1)
    return n - 1''')
    assert fixed != engine
    t.ref("sheetcalc/engine.py", fixed)
    t.answer("The report summed the wrong columns: engine.col_to_index treated column letters as plain base 26, so "
             "AA became 0 (same as A) and AE became 4 (E). The data has 30 day columns (B..AE), so SUM(B2:AE2) only "
             "covered B..E. Fixed col_to_index to use bijective base 26 (A=1..Z=26, minus one at the end).\n")
    t.gr("tests/test_zz_hidden.py", d('''
        import os
        import unittest

        from sheetcalc import Sheet, evaluate, parse_ref, report
        from sheetcalc.engine import col_to_index, index_to_col

        DATA = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "june.csv")


        class HiddenTest(unittest.TestCase):
            def test_columns(self):
                cases = {"A": 0, "Z": 25, "AA": 26, "AB": 27, "AZ": 51, "BA": 52, "ZZ": 701, "AAA": 702, "abc": 730}
                for letters, idx in cases.items():
                    self.assertEqual(col_to_index(letters), idx, letters)
                for i in range(0, 2000, 7):
                    self.assertEqual(col_to_index(index_to_col(i)), i)
                self.assertEqual(parse_ref("AE2"), (1, 30))
                self.assertEqual(parse_ref("$AA$10"), (9, 26))

            def test_cells_past_z(self):
                s = Sheet()
                s.set("A1", 1)
                s.set("AA1", 2)
                s.set("Z1", 4)
                s.set("AB1", 8)
                self.assertEqual(s.get("A1"), 1)
                self.assertEqual(evaluate(s, "=SUM(Z1:AB1)"), 14)
                self.assertEqual(evaluate(s, "=AA1*10"), 20)

            def test_report(self):
                stations, total = report.summarize(DATA)
                self.assertEqual(stations, %(all)r)
                self.assertEqual(total, %(grand)d)


        if __name__ == "__main__":
            unittest.main()
        ''') % {"all": exp, "grand": grand})
    t.register(
        name="Debug wrong report totals caused by a column-letter bug deep in a ~1300-line formula engine",
        category="long-context", tags=["python", "debugging", "long", "root-cause", "large-file"], difficulty="hard",
        prompt=("tests/test_report.py fails: the June station report has wrong totals. Find the root cause and fix "
                "it. Do not modify the tests or the data in data/, and do not special-case this report or its "
                "values. When done, explain the root cause in two or three sentences. All tests must pass "
                "(`python -m unittest discover -s tests -t .`)."),
        strictDiff=True,
        allowedChangedFiles=["sheetcalc/**", "tests/test_*.py"],
        forbiddenChangedFiles=["tests/test_engine.py", "tests/test_report.py", "data/**", "sheetcalc/report.py",
                               "sheetcalc/csvio.py"],
        expectedChangedFiles=["sheetcalc/engine.py"],
        verificationCommands=[cmd("unittest", *UNITTEST)],
        fileChecks=[{"id": "engine-focused", "path": "sheetcalc/engine.py", "maxChangedLines": 40}],
        answerChecks=[{"id": "explains-cause", "anyOf": [r"col_to_index", r"column", r"base.?26", r"\bAA\b"],
                       "regex": True}],
    )
