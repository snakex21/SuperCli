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

_REF_RE = re.compile(r"^\$?([A-Za-z]{1,3})\$?([0-9]{1,7})$")


def col_to_index(letters):
    """Column letters to a 0-based index: A -> 0, Z -> 25, AA -> 26, AZ -> 51, BA -> 52."""
    n = 0
    for ch in letters.upper():
        n = n * 26 + (ord(ch) - ord("A") + 1)
    return n - 1


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
    (?P<ws>\s+)
  | (?P<num>(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)
  | (?P<str>"(?:[^"]|"")*")
  | (?P<ref>\$?[A-Za-z]{1,3}\$?\d+)(?![A-Za-z0-9_(.])
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


def fn_km2mi(args):
    """KM2MI(x): kilometres to miles."""
    if len(args) != 1:
        raise FormulaError("KM2MI takes exactly one argument")
    x = to_number(scalar(args[0], "KM2MI argument"))
    return float(x / 1.609344)


def fn_mi2km(args):
    """MI2KM(x): miles to kilometres."""
    if len(args) != 1:
        raise FormulaError("MI2KM takes exactly one argument")
    x = to_number(scalar(args[0], "MI2KM argument"))
    return float(x * 1.609344)


def fn_kg2lb(args):
    """KG2LB(x): kilograms to pounds."""
    if len(args) != 1:
        raise FormulaError("KG2LB takes exactly one argument")
    x = to_number(scalar(args[0], "KG2LB argument"))
    return float(x / 0.45359237)


def fn_lb2kg(args):
    """LB2KG(x): pounds to kilograms."""
    if len(args) != 1:
        raise FormulaError("LB2KG takes exactly one argument")
    x = to_number(scalar(args[0], "LB2KG argument"))
    return float(x * 0.45359237)


def fn_c2f(args):
    """C2F(x): Celsius to Fahrenheit."""
    if len(args) != 1:
        raise FormulaError("C2F takes exactly one argument")
    x = to_number(scalar(args[0], "C2F argument"))
    return float(x * 9 / 5 + 32)


def fn_f2c(args):
    """F2C(x): Fahrenheit to Celsius."""
    if len(args) != 1:
        raise FormulaError("F2C takes exactly one argument")
    x = to_number(scalar(args[0], "F2C argument"))
    return float((x - 32) * 5 / 9)


def fn_l2gal(args):
    """L2GAL(x): litres to US gallons."""
    if len(args) != 1:
        raise FormulaError("L2GAL takes exactly one argument")
    x = to_number(scalar(args[0], "L2GAL argument"))
    return float(x / 3.785411784)


def fn_gal2l(args):
    """GAL2L(x): US gallons to litres."""
    if len(args) != 1:
        raise FormulaError("GAL2L takes exactly one argument")
    x = to_number(scalar(args[0], "GAL2L argument"))
    return float(x * 3.785411784)


def fn_m2ft(args):
    """M2FT(x): metres to feet."""
    if len(args) != 1:
        raise FormulaError("M2FT takes exactly one argument")
    x = to_number(scalar(args[0], "M2FT argument"))
    return float(x / 0.3048)


def fn_ft2m(args):
    """FT2M(x): feet to metres."""
    if len(args) != 1:
        raise FormulaError("FT2M takes exactly one argument")
    x = to_number(scalar(args[0], "FT2M argument"))
    return float(x * 0.3048)


def fn_cm2in(args):
    """CM2IN(x): centimetres to inches."""
    if len(args) != 1:
        raise FormulaError("CM2IN takes exactly one argument")
    x = to_number(scalar(args[0], "CM2IN argument"))
    return float(x / 2.54)


def fn_in2cm(args):
    """IN2CM(x): inches to centimetres."""
    if len(args) != 1:
        raise FormulaError("IN2CM takes exactly one argument")
    x = to_number(scalar(args[0], "IN2CM argument"))
    return float(x * 2.54)


def fn_kwh2mj(args):
    """KWH2MJ(x): kilowatt-hours to megajoules."""
    if len(args) != 1:
        raise FormulaError("KWH2MJ takes exactly one argument")
    x = to_number(scalar(args[0], "KWH2MJ argument"))
    return float(x * 3.6)


def fn_mj2kwh(args):
    """MJ2KWH(x): megajoules to kilowatt-hours."""
    if len(args) != 1:
        raise FormulaError("MJ2KWH takes exactly one argument")
    x = to_number(scalar(args[0], "MJ2KWH argument"))
    return float(x / 3.6)


def fn_ha2ac(args):
    """HA2AC(x): hectares to acres."""
    if len(args) != 1:
        raise FormulaError("HA2AC takes exactly one argument")
    x = to_number(scalar(args[0], "HA2AC argument"))
    return float(x / 0.40468564224)


def fn_ac2ha(args):
    """AC2HA(x): acres to hectares."""
    if len(args) != 1:
        raise FormulaError("AC2HA takes exactly one argument")
    x = to_number(scalar(args[0], "AC2HA argument"))
    return float(x * 0.40468564224)


def fn_kmh2ms(args):
    """KMH2MS(x): km/h to m/s."""
    if len(args) != 1:
        raise FormulaError("KMH2MS takes exactly one argument")
    x = to_number(scalar(args[0], "KMH2MS argument"))
    return float(x / 3.6)


def fn_ms2kmh(args):
    """MS2KMH(x): m/s to km/h."""
    if len(args) != 1:
        raise FormulaError("MS2KMH takes exactly one argument")
    x = to_number(scalar(args[0], "MS2KMH argument"))
    return float(x * 3.6)


def fn_kn2kmh(args):
    """KN2KMH(x): knots to km/h."""
    if len(args) != 1:
        raise FormulaError("KN2KMH takes exactly one argument")
    x = to_number(scalar(args[0], "KN2KMH argument"))
    return float(x * 1.852)


def fn_kmh2kn(args):
    """KMH2KN(x): km/h to knots."""
    if len(args) != 1:
        raise FormulaError("KMH2KN takes exactly one argument")
    x = to_number(scalar(args[0], "KMH2KN argument"))
    return float(x / 1.852)


def fn_bar2psi(args):
    """BAR2PSI(x): bar to psi."""
    if len(args) != 1:
        raise FormulaError("BAR2PSI takes exactly one argument")
    x = to_number(scalar(args[0], "BAR2PSI argument"))
    return float(x * 14.503773773)


def fn_psi2bar(args):
    """PSI2BAR(x): psi to bar."""
    if len(args) != 1:
        raise FormulaError("PSI2BAR takes exactly one argument")
    x = to_number(scalar(args[0], "PSI2BAR argument"))
    return float(x / 14.503773773)


def fn_deg2rad(args):
    """DEG2RAD(x): degrees to radians."""
    if len(args) != 1:
        raise FormulaError("DEG2RAD takes exactly one argument")
    x = to_number(scalar(args[0], "DEG2RAD argument"))
    return float(x * math.pi / 180)


def fn_rad2deg(args):
    """RAD2DEG(x): radians to degrees."""
    if len(args) != 1:
        raise FormulaError("RAD2DEG takes exactly one argument")
    x = to_number(scalar(args[0], "RAD2DEG argument"))
    return float(x * 180 / math.pi)


def fn_h2min(args):
    """H2MIN(x): hours to minutes."""
    if len(args) != 1:
        raise FormulaError("H2MIN takes exactly one argument")
    x = to_number(scalar(args[0], "H2MIN argument"))
    return float(x * 60)


def fn_min2h(args):
    """MIN2H(x): minutes to hours."""
    if len(args) != 1:
        raise FormulaError("MIN2H takes exactly one argument")
    x = to_number(scalar(args[0], "MIN2H argument"))
    return float(x / 60)


def fn_d2h(args):
    """D2H(x): days to hours."""
    if len(args) != 1:
        raise FormulaError("D2H takes exactly one argument")
    x = to_number(scalar(args[0], "D2H argument"))
    return float(x * 24)


def fn_h2d(args):
    """H2D(x): hours to days."""
    if len(args) != 1:
        raise FormulaError("H2D takes exactly one argument")
    x = to_number(scalar(args[0], "H2D argument"))
    return float(x / 24)


def fn_kb2b(args):
    """KB2B(x): kibibytes to bytes."""
    if len(args) != 1:
        raise FormulaError("KB2B takes exactly one argument")
    x = to_number(scalar(args[0], "KB2B argument"))
    return float(x * 1024)


def fn_b2kb(args):
    """B2KB(x): bytes to kibibytes."""
    if len(args) != 1:
        raise FormulaError("B2KB takes exactly one argument")
    x = to_number(scalar(args[0], "B2KB argument"))
    return float(x / 1024)


def fn_sqrt(args):
    """SQRT(x): square root."""
    if len(args) != 1:
        raise FormulaError("SQRT takes exactly one argument")
    x = to_number(scalar(args[0], "SQRT argument"))
    return float(math.sqrt(x) if x >= 0 else _fail('SQRT of a negative number'))


def fn_exp(args):
    """EXP(x): e to the power x."""
    if len(args) != 1:
        raise FormulaError("EXP takes exactly one argument")
    x = to_number(scalar(args[0], "EXP argument"))
    return float(math.exp(x))


def fn_ln(args):
    """LN(x): natural logarithm."""
    if len(args) != 1:
        raise FormulaError("LN takes exactly one argument")
    x = to_number(scalar(args[0], "LN argument"))
    return float(math.log(x) if x > 0 else _fail('LN domain'))


def fn_log10(args):
    """LOG10(x): base-10 logarithm."""
    if len(args) != 1:
        raise FormulaError("LOG10 takes exactly one argument")
    x = to_number(scalar(args[0], "LOG10 argument"))
    return float(math.log10(x) if x > 0 else _fail('LOG10 domain'))


def fn_sign(args):
    """SIGN(x): sign of x (-1, 0 or 1)."""
    if len(args) != 1:
        raise FormulaError("SIGN takes exactly one argument")
    x = to_number(scalar(args[0], "SIGN argument"))
    return float((x > 0) - (x < 0))


def fn_int(args):
    """INT(x): round down to an integer."""
    if len(args) != 1:
        raise FormulaError("INT takes exactly one argument")
    x = to_number(scalar(args[0], "INT argument"))
    return float(math.floor(x))


def fn_trunc(args):
    """TRUNC(x): truncate toward zero."""
    if len(args) != 1:
        raise FormulaError("TRUNC takes exactly one argument")
    x = to_number(scalar(args[0], "TRUNC argument"))
    return float(math.trunc(x))


def fn_ceiling(args):
    """CEILING(x): round up to an integer."""
    if len(args) != 1:
        raise FormulaError("CEILING takes exactly one argument")
    x = to_number(scalar(args[0], "CEILING argument"))
    return float(math.ceil(x))


def fn_floor(args):
    """FLOOR(x): round down to an integer."""
    if len(args) != 1:
        raise FormulaError("FLOOR takes exactly one argument")
    x = to_number(scalar(args[0], "FLOOR argument"))
    return float(math.floor(x))


def fn_even(args):
    """EVEN(x): round away from zero to an even integer."""
    if len(args) != 1:
        raise FormulaError("EVEN takes exactly one argument")
    x = to_number(scalar(args[0], "EVEN argument"))
    return float(math.copysign(math.ceil(abs(x) / 2) * 2, x))


def fn_odd(args):
    """ODD(x): round away from zero to an odd integer."""
    if len(args) != 1:
        raise FormulaError("ODD takes exactly one argument")
    x = to_number(scalar(args[0], "ODD argument"))
    return float(math.copysign(math.ceil((abs(x) - 1) / 2) * 2 + 1, x))


def fn_square(args):
    """SQUARE(x): x squared."""
    if len(args) != 1:
        raise FormulaError("SQUARE takes exactly one argument")
    x = to_number(scalar(args[0], "SQUARE argument"))
    return float(x * x)


def fn_cube(args):
    """CUBE(x): x cubed."""
    if len(args) != 1:
        raise FormulaError("CUBE takes exactly one argument")
    x = to_number(scalar(args[0], "CUBE argument"))
    return float(x * x * x)


def fn_recip(args):
    """RECIP(x): 1 / x."""
    if len(args) != 1:
        raise FormulaError("RECIP takes exactly one argument")
    x = to_number(scalar(args[0], "RECIP argument"))
    return float(1 / x if x else _fail('RECIP of zero'))


def fn_sin(args):
    """SIN(x): sine (radians)."""
    if len(args) != 1:
        raise FormulaError("SIN takes exactly one argument")
    x = to_number(scalar(args[0], "SIN argument"))
    return float(math.sin(x))


def fn_cos(args):
    """COS(x): cosine (radians)."""
    if len(args) != 1:
        raise FormulaError("COS takes exactly one argument")
    x = to_number(scalar(args[0], "COS argument"))
    return float(math.cos(x))


def fn_tan(args):
    """TAN(x): tangent (radians)."""
    if len(args) != 1:
        raise FormulaError("TAN takes exactly one argument")
    x = to_number(scalar(args[0], "TAN argument"))
    return float(math.tan(x))


def fn_fact(args):
    """FACT(x): factorial."""
    if len(args) != 1:
        raise FormulaError("FACT takes exactly one argument")
    x = to_number(scalar(args[0], "FACT argument"))
    return float(float(math.factorial(int(x))) if x >= 0 else _fail('FACT domain'))


def fn_product(args):
    """PRODUCT(values...): product of the numbers. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(math.prod(ns) if ns else 0.0)


def fn_sumsq(args):
    """SUMSQ(values...): sum of squares. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(sum(n * n for n in ns))


def fn_median(args):
    """MEDIAN(values...): median. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(_median(ns))


def fn_varp(args):
    """VARP(values...): population variance. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(_var(ns, 0))


def fn_var(args):
    """VAR(values...): sample variance. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(_var(ns, 1))


def fn_stdevp(args):
    """STDEVP(values...): population standard deviation. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(math.sqrt(_var(ns, 0)))


def fn_stdev(args):
    """STDEV(values...): sample standard deviation. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(math.sqrt(_var(ns, 1)))


def fn_range(args):
    """RANGE(values...): max minus min. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float((max(ns) - min(ns)) if ns else 0.0)


def fn_geomean(args):
    """GEOMEAN(values...): geometric mean. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(math.exp(sum(math.log(n) for n in ns) / len(ns)) if ns and min(ns) > 0 else _fail('GEOMEAN domain'))


def fn_harmean(args):
    """HARMEAN(values...): harmonic mean. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(len(ns) / sum(1 / n for n in ns) if ns and min(ns) > 0 else _fail('HARMEAN domain'))


def fn_avedev(args):
    """AVEDEV(values...): mean absolute deviation. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(sum(abs(n - sum(ns) / len(ns)) for n in ns) / len(ns) if ns else 0.0)


def fn_countpos(args):
    """COUNTPOS(values...): count of positive numbers. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(float(sum(1 for n in ns if n > 0)))


def fn_countneg(args):
    """COUNTNEG(values...): count of negative numbers. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(float(sum(1 for n in ns if n < 0)))


def fn_sumpos(args):
    """SUMPOS(values...): sum of positive numbers. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(sum(n for n in ns if n > 0))


def fn_sumneg(args):
    """SUMNEG(values...): sum of negative numbers. Ranges skip text and empty cells."""
    ns = numbers(args)
    return float(sum(n for n in ns if n < 0))


def fn_trim(args):
    """TRIM(text): strip surrounding spaces and collapse inner runs."""
    if len(args) != 1:
        raise FormulaError("TRIM takes exactly one argument")
    s = to_text(scalar(args[0], "TRIM argument"))
    return ' '.join(s.split())


def fn_proper(args):
    """PROPER(text): capitalise every word."""
    if len(args) != 1:
        raise FormulaError("PROPER takes exactly one argument")
    s = to_text(scalar(args[0], "PROPER argument"))
    return s.title()


def fn_reverse(args):
    """REVERSE(text): reverse the text."""
    if len(args) != 1:
        raise FormulaError("REVERSE takes exactly one argument")
    s = to_text(scalar(args[0], "REVERSE argument"))
    return s[::-1]


def fn_clean(args):
    """CLEAN(text): drop non-printable characters."""
    if len(args) != 1:
        raise FormulaError("CLEAN takes exactly one argument")
    s = to_text(scalar(args[0], "CLEAN argument"))
    return ''.join(ch for ch in s if ch.isprintable())


def fn_initials(args):
    """INITIALS(text): first letter of every word."""
    if len(args) != 1:
        raise FormulaError("INITIALS takes exactly one argument")
    s = to_text(scalar(args[0], "INITIALS argument"))
    return ''.join(w[0].upper() for w in s.split())


def fn_slug(args):
    """SLUG(text): lower-case words joined by dashes."""
    if len(args) != 1:
        raise FormulaError("SLUG takes exactly one argument")
    s = to_text(scalar(args[0], "SLUG argument"))
    return '-'.join(re.findall('[a-z0-9]+', s.lower()))


def fn_words(args):
    """WORDS(text): number of words."""
    if len(args) != 1:
        raise FormulaError("WORDS takes exactly one argument")
    s = to_text(scalar(args[0], "WORDS argument"))
    return float(len(s.split()))


def fn_vowels(args):
    """VOWELS(text): number of vowels."""
    if len(args) != 1:
        raise FormulaError("VOWELS takes exactly one argument")
    s = to_text(scalar(args[0], "VOWELS argument"))
    return float(sum(1 for ch in s.lower() if ch in 'aeiou'))


FUNCTIONS = {
    "SUM": fn_sum,
    "AVERAGE": fn_average,
    "MIN": fn_min,
    "MAX": fn_max,
    "COUNT": fn_count,
    "COUNTA": fn_counta,
    "ROUND": fn_round,
    "ABS": fn_abs,
    "CONCAT": fn_concat,
    "LEN": fn_len,
    "UPPER": fn_upper,
    "LOWER": fn_lower,
    "AND": fn_and,
    "OR": fn_or,
    "NOT": fn_not,
    "MATCH": fn_match,
    "INDEX": fn_index,
    "SUMIF": fn_sumif,
    "COUNTIF": fn_countif,
    "KM2MI": fn_km2mi,
    "MI2KM": fn_mi2km,
    "KG2LB": fn_kg2lb,
    "LB2KG": fn_lb2kg,
    "C2F": fn_c2f,
    "F2C": fn_f2c,
    "L2GAL": fn_l2gal,
    "GAL2L": fn_gal2l,
    "M2FT": fn_m2ft,
    "FT2M": fn_ft2m,
    "CM2IN": fn_cm2in,
    "IN2CM": fn_in2cm,
    "KWH2MJ": fn_kwh2mj,
    "MJ2KWH": fn_mj2kwh,
    "HA2AC": fn_ha2ac,
    "AC2HA": fn_ac2ha,
    "KMH2MS": fn_kmh2ms,
    "MS2KMH": fn_ms2kmh,
    "KN2KMH": fn_kn2kmh,
    "KMH2KN": fn_kmh2kn,
    "BAR2PSI": fn_bar2psi,
    "PSI2BAR": fn_psi2bar,
    "DEG2RAD": fn_deg2rad,
    "RAD2DEG": fn_rad2deg,
    "H2MIN": fn_h2min,
    "MIN2H": fn_min2h,
    "D2H": fn_d2h,
    "H2D": fn_h2d,
    "KB2B": fn_kb2b,
    "B2KB": fn_b2kb,
    "SQRT": fn_sqrt,
    "EXP": fn_exp,
    "LN": fn_ln,
    "LOG10": fn_log10,
    "SIGN": fn_sign,
    "INT": fn_int,
    "TRUNC": fn_trunc,
    "CEILING": fn_ceiling,
    "FLOOR": fn_floor,
    "EVEN": fn_even,
    "ODD": fn_odd,
    "SQUARE": fn_square,
    "CUBE": fn_cube,
    "RECIP": fn_recip,
    "SIN": fn_sin,
    "COS": fn_cos,
    "TAN": fn_tan,
    "FACT": fn_fact,
    "PRODUCT": fn_product,
    "SUMSQ": fn_sumsq,
    "MEDIAN": fn_median,
    "VARP": fn_varp,
    "VAR": fn_var,
    "STDEVP": fn_stdevp,
    "STDEV": fn_stdev,
    "RANGE": fn_range,
    "GEOMEAN": fn_geomean,
    "HARMEAN": fn_harmean,
    "AVEDEV": fn_avedev,
    "COUNTPOS": fn_countpos,
    "COUNTNEG": fn_countneg,
    "SUMPOS": fn_sumpos,
    "SUMNEG": fn_sumneg,
    "TRIM": fn_trim,
    "PROPER": fn_proper,
    "REVERSE": fn_reverse,
    "CLEAN": fn_clean,
    "INITIALS": fn_initials,
    "SLUG": fn_slug,
    "WORDS": fn_words,
    "VOWELS": fn_vowels,
}


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
