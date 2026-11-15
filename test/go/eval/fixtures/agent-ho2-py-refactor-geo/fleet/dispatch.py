"""Pick the closest available vehicle for a job."""

import math
from dataclasses import dataclass


@dataclass
class Vehicle:
    id: str
    lat: float
    lon: float
    available: bool = True


def nearest(vehicles, lat, lon, max_km=None):
    """Closest available vehicle to (lat, lon) as (vehicle, km), or None.

    Ties go to the vehicle listed first.
    """
    best = None
    for v in vehicles:
        if not v.available:
            continue
        dlat = math.radians(v.lat - lat)
        dlon = math.radians(v.lon - lon)
        a = math.sin(dlat / 2) ** 2 + math.cos(math.radians(lat)) * math.cos(math.radians(v.lat)) * math.sin(dlon / 2) ** 2
        km = 12742.0 * math.asin(math.sqrt(a))
        if max_km is not None and km > max_km:
            continue
        if best is None or km < best[1]:
            best = (v, km)
    return best
