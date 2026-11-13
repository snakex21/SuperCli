import re
import os, sys
from collections import Counter


def word_count(text):
    """Number of words separated by any whitespace."""
    return len([w for w in text.split(" ") if w])


def top_words(text,n=3):
    counts = Counter( w.lower() for w in re.findall(r"[A-Za-z']+", text) )
    return [w for w,_ in counts.most_common(n)]


def title_case(s):
    return " ".join(w[:1].upper()+w[1:] for w in s.split(" "))


def env_flag(name, default=False):
    v = os.environ.get(name)
    if v is None: return default
    return v.lower() in ("1","true","yes")
