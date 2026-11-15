"""Trip statistics. A trip is a list of (lat, lon) points in degrees."""

import math


def _haversine(lat1, lon1, lat2, lon2):
    """Great-circle distance in km between two points."""
    p1, p2 = math.radians(lat1), math.radians(lat2)
    dp = p2 - p1
    dl = math.radians(lon2 - lon1)
    a = math.sin(dp / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dl / 2) ** 2
    return 2 * 6371.0 * math.asin(math.sqrt(a))


def trip_length_km(points):
    """Total length of the trip in km."""
    return sum(_haversine(a[0], a[1], b[0], b[1]) for a, b in zip(points, points[1:]))


def average_speed_kmh(points, seconds):
    """Average speed over the trip; 0 for an empty duration."""
    if seconds <= 0:
        return 0.0
    return trip_length_km(points) / (seconds / 3600.0)
