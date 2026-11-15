import unittest

from fleet import dispatch, eta, geofence, report, trips


class FleetTest(unittest.TestCase):
    def test_trip(self):
        self.assertAlmostEqual(trips.trip_length_km([(52.2297, 21.0122), (52.4064, 16.9252), (51.1079, 17.0385)]), 423.050924, places=4)
        self.assertEqual(trips.trip_length_km([(1, 1)]), 0)

    def test_fence(self):
        f = geofence.Fence("centrum", (21.0122, 52.2297), 2500)
        self.assertTrue(f.contains((21.03, 52.24)))
        self.assertFalse(f.contains((21.10, 52.20)))

    def test_dispatch(self):
        vs = [dispatch.Vehicle("a", 50.0647, 19.9450), dispatch.Vehicle("b", 52.40, 16.92)]
        v, km = dispatch.nearest(vs, 52.2297, 21.0122)
        self.assertEqual(v.id, "a")
        self.assertIsNone(dispatch.nearest(vs, 52.2297, 21.0122, max_km=100))

    def test_report(self):
        pings = [
            {"vehicle": "v1", "day": "2027-04-01", "lat": 52.2297, "lng": 21.0122},
            {"vehicle": "v1", "day": "2027-04-01", "lat": 50.0647, "lng": 19.945},
        ]
        self.assertEqual(report.daily_distance(pings), {("v1", "2027-04-01"): 252.0})

    def test_eta(self):
        self.assertEqual(eta.eta_minutes(52.2297, 21.0122, 52.2297, 21.0122), 1)


if __name__ == "__main__":
    unittest.main()
