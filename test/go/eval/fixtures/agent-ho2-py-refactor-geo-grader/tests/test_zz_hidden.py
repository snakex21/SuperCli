import unittest

from fleet import dispatch, eta, geo, geofence, report, trips


class HiddenTest(unittest.TestCase):
    def test_geo(self):
        self.assertAlmostEqual(geo.haversine_km(52.2297, 21.0122, 50.0647, 19.9450), 251.97657791521934, places=6)
        self.assertEqual(geo.haversine_km(10, 20, 10, 20), 0)

    def test_fence_order_and_units(self):
        f = geofence.Fence("krk", (19.945, 50.0647), 1500)
        for pt, want in zip([(19.96, 50.07), (20.1, 50.0), (19.0, 50.5)], [1222.1034147763132, 13203.326383769614, 82772.51759281656]):
            self.assertAlmostEqual(f.distance_m(pt), want, places=3)
        self.assertEqual(geofence.fences_for((19.96, 50.07), [f, geofence.Fence("far", (0.0, 0.0), 10)]), ["krk"])

    def test_report(self):
        got = report.daily_distance([{"vehicle": "v1", "day": "2027-04-01", "lat": 52.78345, "lng": 20.68505}, {"vehicle": "v1", "day": "2027-04-01", "lat": 52.78059, "lng": 20.75717}, {"vehicle": "v1", "day": "2027-04-01", "lat": 52.7632, "lng": 20.70931}, {"vehicle": "v1", "day": "2027-04-01", "lat": 52.77539, "lng": 20.7604}, {"vehicle": "v1", "day": "2027-04-01", "lat": 52.76046, "lng": 20.8331}, {"vehicle": "v1", "day": "2027-04-01", "lat": 52.73998, "lng": 20.84162}, {"vehicle": "v1", "day": "2027-04-02", "lat": 52.70334, "lng": 20.25549}, {"vehicle": "v1", "day": "2027-04-02", "lat": 52.69308, "lng": 20.32517}, {"vehicle": "v1", "day": "2027-04-02", "lat": 52.73457, "lng": 20.33194}, {"vehicle": "v1", "day": "2027-04-02", "lat": 52.75241, "lng": 20.33067}, {"vehicle": "v1", "day": "2027-04-02", "lat": 52.72869, "lng": 20.34122}, {"vehicle": "v1", "day": "2027-04-02", "lat": 52.72884, "lng": 20.31754}, {"vehicle": "v2", "day": "2027-04-01", "lat": 52.78383, "lng": 19.42001}, {"vehicle": "v2", "day": "2027-04-01", "lat": 52.79073, "lng": 19.49819}, {"vehicle": "v2", "day": "2027-04-01", "lat": 52.75809, "lng": 19.4562}, {"vehicle": "v2", "day": "2027-04-01", "lat": 52.75416, "lng": 19.37622}, {"vehicle": "v2", "day": "2027-04-01", "lat": 52.78927, "lng": 19.42891}, {"vehicle": "v2", "day": "2027-04-01", "lat": 52.83794, "lng": 19.40726}, {"vehicle": "v2", "day": "2027-04-02", "lat": 52.25581, "lng": 20.27265}, {"vehicle": "v2", "day": "2027-04-02", "lat": 52.28262, "lng": 20.28074}, {"vehicle": "v2", "day": "2027-04-02", "lat": 52.23333, "lng": 20.34998}, {"vehicle": "v2", "day": "2027-04-02", "lat": 52.21644, "lng": 20.29196}, {"vehicle": "v2", "day": "2027-04-02", "lat": 52.22854, "lng": 20.27545}, {"vehicle": "v2", "day": "2027-04-02", "lat": 52.18473, "lng": 20.29049}])
        self.assertEqual(sorted([list(k), v] for k, v in got.items()), [[["v1", "2027-04-01"], 19.8], [["v1", "2027-04-02"], 15.8], [["v2", "2027-04-01"], 26.2], [["v2", "2027-04-02"], 21.4]])

    def test_dispatch(self):
        vs = [dispatch.Vehicle(i, la, lo, av) for i, la, lo, av in [('a', 50.3, 19.1, True), ('b', 50.1, 20.0, False), ('c', 50.06, 19.9, True), ('d', 49.8, 19.95, True)]]
        v, km = dispatch.nearest(vs, 50.0647, 19.9450)
        self.assertEqual(v.id, 'c')
        self.assertAlmostEqual(km, 3.254425822814235, places=6)

    def test_trips_eta(self):
        self.assertAlmostEqual(trips.trip_length_km([(52.2297, 21.0122), (52.4064, 16.9252), (51.1079, 17.0385)]), 423.0509242540471, places=6)
        self.assertAlmostEqual(trips.average_speed_kmh([(52.2297, 21.0122), (52.4064, 16.9252), (51.1079, 17.0385)], 7200), 423.0509242540471 / 2, places=6)
        self.assertEqual(eta.eta_minutes(52.2297, 21.0122, 50.0647, 19.9450, kmh=60), 252)


if __name__ == "__main__":
    unittest.main()
