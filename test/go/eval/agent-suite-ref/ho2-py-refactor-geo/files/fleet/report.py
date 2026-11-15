"""Daily distance report from raw GPS pings."""

from fleet.geo import haversine_km


def daily_distance(pings):
    """pings: dicts with vehicle, day, lat, lng in time order.

    Returns {(vehicle, day): km rounded to 0.1}.
    """
    last = {}
    totals = {}
    for p in pings:
        key = (p["vehicle"], p["day"])
        if key in last:
            totals[key] = totals.get(key, 0.0) + haversine_km(last[key]["lat"], last[key]["lng"], p["lat"], p["lng"])
        else:
            totals.setdefault(key, 0.0)
        last[key] = p
    return {k: round(v, 1) for k, v in totals.items()}


def render(pings):
    rows = sorted(daily_distance(pings).items())
    return "\n".join("%s %s %.1f km" % (veh, day, km) for (veh, day), km in rows) + "\n"
