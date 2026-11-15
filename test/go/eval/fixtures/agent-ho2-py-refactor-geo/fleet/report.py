"""Daily distance report from raw GPS pings."""

import math


def _km(p, q):
    la1, la2 = math.radians(p["lat"]), math.radians(q["lat"])
    dla = la2 - la1
    dlo = math.radians(q["lng"] - p["lng"])
    s = math.sin(dla / 2) ** 2 + math.cos(la1) * math.cos(la2) * math.sin(dlo / 2) ** 2
    return 6371.0 * 2 * math.asin(math.sqrt(s))


def daily_distance(pings):
    """pings: dicts with vehicle, day, lat, lng in time order.

    Returns {(vehicle, day): km rounded to 0.1}.
    """
    last = {}
    totals = {}
    for p in pings:
        key = (p["vehicle"], p["day"])
        if key in last:
            totals[key] = totals.get(key, 0.0) + _km(last[key], p)
        else:
            totals.setdefault(key, 0.0)
        last[key] = p
    return {k: round(v, 1) for k, v in totals.items()}


def render(pings):
    rows = sorted(daily_distance(pings).items())
    return "\n".join("%s %s %.1f km" % (veh, day, km) for (veh, day), km in rows) + "\n"
