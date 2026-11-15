"""Trip statistics. A trip is a list of (lat, lon) points in degrees."""

from fleet.geo import haversine_km


def trip_length_km(points):
    """Total length of the trip in km."""
    return sum(haversine_km(a[0], a[1], b[0], b[1]) for a, b in zip(points, points[1:]))


def average_speed_kmh(points, seconds):
    """Average speed over the trip; 0 for an empty duration."""
    if seconds <= 0:
        return 0.0
    return trip_length_km(points) / (seconds / 3600.0)
