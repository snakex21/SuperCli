"""Circular geofences. Coordinates here are GeoJSON-style (lon, lat) pairs."""

from fleet.geo import haversine_km


class Fence:
    def __init__(self, name, center, radius_m):
        self.name = name
        self.center = center  # (lon, lat)
        self.radius_m = radius_m

    def distance_m(self, point):
        """Distance in metres from the fence centre to point (lon, lat)."""
        lon1, lat1 = self.center
        lon2, lat2 = point
        return haversine_km(lat1, lon1, lat2, lon2) * 1000.0

    def contains(self, point):
        return self.distance_m(point) <= self.radius_m


def fences_for(point, fences):
    """Names of the fences containing point, sorted."""
    return sorted(f.name for f in fences if f.contains(point))
