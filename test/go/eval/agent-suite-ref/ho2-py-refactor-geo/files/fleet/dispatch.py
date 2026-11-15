"""Pick the closest available vehicle for a job."""

from dataclasses import dataclass

from fleet.geo import haversine_km


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
        km = haversine_km(lat, lon, v.lat, v.lon)
        if max_km is not None and km > max_km:
            continue
        if best is None or km < best[1]:
            best = (v, km)
    return best
