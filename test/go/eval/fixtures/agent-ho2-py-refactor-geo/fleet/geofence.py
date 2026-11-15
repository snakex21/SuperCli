"""Circular geofences. Coordinates here are GeoJSON-style (lon, lat) pairs."""

from math import atan2, cos, radians, sin, sqrt

EARTH_M = 6371000.0


def _dist_m(a, b):
    lon1, lat1 = a
    lon2, lat2 = b
    phi1, phi2 = radians(lat1), radians(lat2)
    dphi = radians(lat2 - lat1)
    dlmb = radians(lon2 - lon1)
    h = sin(dphi / 2) ** 2 + cos(phi1) * cos(phi2) * sin(dlmb / 2) ** 2
    return EARTH_M * 2 * atan2(sqrt(h), sqrt(1 - h))


class Fence:
    def __init__(self, name, center, radius_m):
        self.name = name
        self.center = center  # (lon, lat)
        self.radius_m = radius_m

    def distance_m(self, point):
        """Distance in metres from the fence centre to point (lon, lat)."""
        return _dist_m(self.center, point)

    def contains(self, point):
        return self.distance_m(point) <= self.radius_m


def fences_for(point, fences):
    """Names of the fences containing point, sorted."""
    return sorted(f.name for f in fences if f.contains(point))
