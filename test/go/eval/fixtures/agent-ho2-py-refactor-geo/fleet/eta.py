"""Arrival estimates."""

from fleet.trips import _haversine

CITY_KMH = 28.0


def eta_minutes(lat, lon, dest_lat, dest_lon, kmh=CITY_KMH):
    """Straight-line ETA in whole minutes (rounded up, at least 1)."""
    km = _haversine(lat, lon, dest_lat, dest_lon)
    minutes = km / kmh * 60
    return max(1, int(minutes) + (0 if minutes == int(minutes) else 1))
