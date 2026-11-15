"""Multi-file refactors: Python (deduplicate a formula with mismatched conventions) and
JavaScript (positional parameters -> options object with falsy-value traps)."""
import json
import math

from common import Task, d, cmd, py_filler, js_filler, dump_json, UNITTEST, sub


def _hav_km(lat1, lon1, lat2, lon2):
    p1, p2 = math.radians(lat1), math.radians(lat2)
    a = math.sin((p2 - p1) / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(math.radians(lon2 - lon1) / 2) ** 2
    return 2 * 6371.0 * math.asin(math.sqrt(a))


# ------------------------------------------------------------ Python: one haversine for the whole package
def py_geo():
    t = Task("py-refactor-geo")
    trips = d('''
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
        ''')
    geofence = d('''
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
        ''')
    dispatch = d('''
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
        ''')
    report = d('''
        """Daily distance report from raw GPS pings."""

        import math


        def _km(p, q):
            la1, la2 = math.radians(p["lat"]), math.radians(q["lat"])
            dla = la2 - la1
            dlo = math.radians(q["lng"] - p["lng"])
            s = math.sin(dla / 2) ** 2 + math.cos(la1) * math.cos(la2) * math.sin(dlo / 2) ** 2
            return 6371.0 * 2 * math.asin(math.sqrt(s))


        def daily_distance(pings):
            """pings: dicts with vehicle, day, lat, lng in time order.

            Returns {(vehicle, day): km rounded to 0.1}.
            """
            last = {}
            totals = {}
            for p in pings:
                key = (p["vehicle"], p["day"])
                if key in last:
                    totals[key] = totals.get(key, 0.0) + _km(last[key], p)
                else:
                    totals.setdefault(key, 0.0)
                last[key] = p
            return {k: round(v, 1) for k, v in totals.items()}


        def render(pings):
            rows = sorted(daily_distance(pings).items())
            return "\\n".join("%s %s %.1f km" % (veh, day, km) for (veh, day), km in rows) + "\\n"
        ''')
    eta = d('''
        """Arrival estimates."""

        from fleet.trips import _haversine

        CITY_KMH = 28.0


        def eta_minutes(lat, lon, dest_lat, dest_lon, kmh=CITY_KMH):
            """Straight-line ETA in whole minutes (rounded up, at least 1)."""
            km = _haversine(lat, lon, dest_lat, dest_lon)
            minutes = km / kmh * 60
            return max(1, int(minutes) + (0 if minutes == int(minutes) else 1))
        ''')
    units = d('''
        """Unit helpers."""

        KM_PER_MILE = 1.609344


        def km_to_miles(km):
            return km / KM_PER_MILE


        def kmh_to_ms(kmh):
            return kmh / 3.6
        ''')
    t.fx("fleet/__init__.py", "")
    t.fx("fleet/trips.py", trips)
    t.fx("fleet/geofence.py", geofence)
    t.fx("fleet/dispatch.py", dispatch)
    t.fx("fleet/report.py", report)
    t.fx("fleet/eta.py", eta)
    t.fx("fleet/units.py", units)

    waw, krk = (52.2297, 21.0122), (50.0647, 19.9450)
    trip = [(52.2297, 21.0122), (52.4064, 16.9252), (51.1079, 17.0385)]
    trip_km = _hav_km(*trip[0], *trip[1]) + _hav_km(*trip[1], *trip[2])
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_fleet.py", d('''
        import unittest

        from fleet import dispatch, eta, geofence, report, trips


        class FleetTest(unittest.TestCase):
            def test_trip(self):
                self.assertAlmostEqual(trips.trip_length_km(%(trip)r), %(trip_km).6f, places=4)
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
                    {"vehicle": "v1", "day": "2027-04-01", "lat": %(wlat)r, "lng": %(wlon)r},
                    {"vehicle": "v1", "day": "2027-04-01", "lat": %(klat)r, "lng": %(klon)r},
                ]
                self.assertEqual(report.daily_distance(pings), {("v1", "2027-04-01"): %(wk).1f})

            def test_eta(self):
                self.assertEqual(eta.eta_minutes(52.2297, 21.0122, 52.2297, 21.0122), 1)


        if __name__ == "__main__":
            unittest.main()
        ''') % {"trip": trip, "trip_km": trip_km, "wlat": waw[0], "wlon": waw[1], "klat": krk[0], "klon": krk[1],
                "wk": round(_hav_km(*waw, *krk), 1)})
    t.fx("README.md", "# fleet\n\nVehicle telematics helpers. Tests: `python -m unittest discover -s tests -t .`.\n")
    py_filler(t, "fleet/billing", count=4)
    py_filler(t, "fleet/drivers", count=3)

    geo = d('''
        """Geodesy helpers shared by the fleet package."""

        import math

        EARTH_RADIUS_KM = 6371.0


        def haversine_km(lat1, lon1, lat2, lon2):
            """Great-circle distance in km between two (lat, lon) points in degrees."""
            p1, p2 = math.radians(lat1), math.radians(lat2)
            dp = p2 - p1
            dl = math.radians(lon2 - lon1)
            a = math.sin(dp / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dl / 2) ** 2
            return 2 * EARTH_RADIUS_KM * math.asin(math.sqrt(a))
        ''')
    t.ref("fleet/geo.py", geo)
    t.ref("fleet/trips.py", sub(trips, (trips[trips.index("import math"):trips.index("def trip_length_km")],
                                        "from fleet.geo import haversine_km\n\n\n"),
                                ("_haversine(a[0], a[1], b[0], b[1])", "haversine_km(a[0], a[1], b[0], b[1])")))
    t.ref("fleet/geofence.py", sub(geofence, (geofence[geofence.index("from math import"):geofence.index("class Fence")],
                                              "from fleet.geo import haversine_km\n\n\n"),
                                   ("return _dist_m(self.center, point)",
                                    "lon1, lat1 = self.center\n        lon2, lat2 = point\n"
                                    "        return haversine_km(lat1, lon1, lat2, lon2) * 1000.0")))
    t.ref("fleet/dispatch.py", sub(dispatch, ("import math\nfrom dataclasses import dataclass",
                                              "from dataclasses import dataclass\n\nfrom fleet.geo import haversine_km"),
                                   (dispatch[dispatch.index("        dlat ="):dispatch.index("        if max_km")],
                                    "        km = haversine_km(lat, lon, v.lat, v.lon)\n")))
    t.ref("fleet/report.py", sub(report, (report[report.index("import math"):report.index("def daily_distance")],
                                          "from fleet.geo import haversine_km\n\n\n"),
                                 ("_km(last[key], p)", 'haversine_km(last[key]["lat"], last[key]["lng"], p["lat"], p["lng"])')))
    t.ref("fleet/eta.py", sub(eta, ("from fleet.trips import _haversine", "from fleet.geo import haversine_km"),
                              ("km = _haversine(", "km = haversine_km(")))
    t.answer("Added fleet/geo.py with haversine_km and switched trips, geofence (which keeps its (lon, lat) order and "
             "metres), dispatch, report (lat/lng dict keys) and eta (it imported the private trips helper) to it. "
             "The four private copies are gone; behaviour is unchanged and the tests pass.\n")

    # Hidden expectations, computed with the original formula.
    fence_center = (19.9450, 50.0647)  # (lon, lat)
    fence_pts = [(19.9600, 50.0700), (20.1000, 50.0000), (19.0000, 50.5000)]
    fence_m = [_hav_km(fence_center[1], fence_center[0], p[1], p[0]) * 1000 for p in fence_pts]
    pings = []
    rng = t.rng
    for veh in ("v1", "v2"):
        for day in ("2027-04-01", "2027-04-02"):
            lat, lon = 52.0 + rng.random(), 19.0 + rng.random() * 2
            for _ in range(6):
                lat += rng.uniform(-0.05, 0.05)
                lon += rng.uniform(-0.08, 0.08)
                pings.append({"vehicle": veh, "day": day, "lat": round(lat, 5), "lng": round(lon, 5)})
    last, totals = {}, {}
    for p in pings:
        key = (p["vehicle"], p["day"])
        if key in last:
            totals[key] = totals.get(key, 0.0) + _hav_km(last[key]["lat"], last[key]["lng"], p["lat"], p["lng"])
        else:
            totals.setdefault(key, 0.0)
        last[key] = p
    daily = sorted([list(k), round(v, 1)] for k, v in totals.items())
    vehicles = [("a", 50.30, 19.10, True), ("b", 50.10, 20.00, False), ("c", 50.06, 19.90, True), ("d", 49.80, 19.95, True)]
    near = min((_hav_km(50.0647, 19.9450, la, lo), vid) for vid, la, lo, av in vehicles if av)
    t.gr("tests/test_zz_hidden.py", d('''
        import unittest

        from fleet import dispatch, eta, geo, geofence, report, trips


        class HiddenTest(unittest.TestCase):
            def test_geo(self):
                self.assertAlmostEqual(geo.haversine_km(52.2297, 21.0122, 50.0647, 19.9450), %(wk)r, places=6)
                self.assertEqual(geo.haversine_km(10, 20, 10, 20), 0)

            def test_fence_order_and_units(self):
                f = geofence.Fence("krk", %(center)r, 1500)
                for pt, want in zip(%(pts)r, %(meters)r):
                    self.assertAlmostEqual(f.distance_m(pt), want, places=3)
                self.assertEqual(geofence.fences_for(%(p0)r, [f, geofence.Fence("far", (0.0, 0.0), 10)]), ["krk"])

            def test_report(self):
                got = report.daily_distance(%(pings)s)
                self.assertEqual(sorted([list(k), v] for k, v in got.items()), %(daily)s)

            def test_dispatch(self):
                vs = [dispatch.Vehicle(i, la, lo, av) for i, la, lo, av in %(vehicles)r]
                v, km = dispatch.nearest(vs, 50.0647, 19.9450)
                self.assertEqual(v.id, %(nid)r)
                self.assertAlmostEqual(km, %(nkm)r, places=6)

            def test_trips_eta(self):
                self.assertAlmostEqual(trips.trip_length_km(%(trip)r), %(trip_km)r, places=6)
                self.assertAlmostEqual(trips.average_speed_kmh(%(trip)r, 7200), %(trip_km)r / 2, places=6)
                self.assertEqual(eta.eta_minutes(52.2297, 21.0122, 50.0647, 19.9450, kmh=60), %(eta)d)


        if __name__ == "__main__":
            unittest.main()
        ''') % {"wk": _hav_km(*waw, *krk), "center": fence_center, "pts": fence_pts, "meters": fence_m,
                "p0": fence_pts[0], "pings": json.dumps(pings), "daily": json.dumps(daily), "vehicles": vehicles,
                "nid": near[1], "nkm": near[0], "trip": trip, "trip_km": trip_km,
                "eta": math.ceil(_hav_km(*waw, *krk) / 60 * 60)})
    formula = r"\b(asin|atan2|sin|cos|sqrt)\s*\("
    t.register(
        name="Consolidate four haversine copies with different conventions (lon/lat order, metres, dict keys)",
        category="multi-file", tags=["python", "refactor", "multi-file", "dedupe"], difficulty="hard",
        prompt=("The fleet package has four private copies of the haversine great-circle distance formula "
                "(in trips.py, geofence.py, dispatch.py and report.py). Consolidate them: create fleet/geo.py with "
                "`haversine_km(lat1, lon1, lat2, lon2) -> float` (Earth radius 6371 km) and make every module use it, "
                "deleting the private copies. The behaviour of every public function must stay exactly the same. "
                "Do not modify the existing tests; they must keep passing."),
        strictDiff=True,
        allowedChangedFiles=["fleet/*.py", "tests/test_*.py"],
        forbiddenChangedFiles=["tests/test_fleet.py", "fleet/units.py", "fleet/billing/**", "fleet/drivers/**"],
        expectedChangedFiles=["fleet/geo.py", "fleet/trips.py", "fleet/geofence.py", "fleet/dispatch.py", "fleet/report.py"],
        verificationCommands=[cmd("unittest", *UNITTEST)],
        fileChecks=[
            {"id": "geo-module", "path": "fleet/geo.py", "regex": [r"def haversine_km\(\s*lat1\s*,\s*lon1\s*,\s*lat2\s*,\s*lon2"],
             "critical": True},
        ] + [{"id": "no-copy-" + m, "path": "fleet/%s.py" % m, "regex": [r"haversine_km"], "notRegex": [formula],
              "critical": True} for m in ("trips", "geofence", "dispatch", "report")],
    )


# ------------------------------------------------------------ JS: positional parameters -> options object
def js_options():
    t = Task("js-refactor-options")
    t.fx("package.json", dump_json({"name": "svcmesh", "version": "3.1.0", "private": True, "type": "module",
                                    "scripts": {"test": "node --test"}}))
    client = d('''
        // createClient(host, port, timeoutMs, retries, tls) describes a connection to
        // an internal service. Omitted (undefined) arguments take the defaults:
        // port 80 (443 with tls), timeoutMs 5000 (0 = no timeout), retries 3, tls false.
        export function createClient(host, port, timeoutMs, retries, tls) {
          if (!host) {
            throw new TypeError("createClient: host is required");
          }
          const secure = tls === true;
          const p = port === undefined ? (secure ? 443 : 80) : port;
          return Object.freeze({
            host,
            port: p,
            timeoutMs: timeoutMs === undefined ? 5000 : timeoutMs,
            retries: retries === undefined ? 3 : retries,
            tls: secure,
            url(path = "/") {
              return `${secure ? "https" : "http"}://${host}:${p}${path}`;
            },
          });
        }
        ''')
    pool = d('''
        import { createClient } from "./client.js";

        // createClientPool(size, host, port) returns `size` identical clients.
        export function createClientPool(size, host, port) {
          const out = [];
          for (let i = 0; i < size; i++) {
            out.push(createClient(host, port));
          }
          return out;
        }
        ''')
    t.fx("net/client.js", client)
    t.fx("net/pool.js", pool)
    services = {
        "services/billing.js": ('import { createClient } from "../net/client.js";\n\n'
                                '// Payments must never be retried automatically.\n'
                                'export const billingClient = () => createClient("billing.internal", 8443, 2000, 0, true);\n'),
        "services/search.js": ('import { createClient } from "../net/client.js";\n\n'
                               'export function searchClient() {\n  return createClient("search.internal", undefined, 750);\n}\n'),
        "services/geo.js": ('import { createClient } from "../net/client.js";\n\nconst GEO_HOST = "geo.internal";\n\n'
                            'export function geoClient() {\n  return createClient(GEO_HOST, undefined, undefined, 5);\n}\n'),
        "services/mail.js": ('import { createClient } from "../net/client.js";\n\n'
                             '// The relay streams large batches: no timeout.\n'
                             'export function mailClient() {\n  return createClient("smtp-relay.internal", 2525, 0);\n}\n'),
        "services/auth.js": ('import { createClient } from "../net/client.js";\n\n'
                             '// cfg: { host, port?, timeoutMs?, retries?, tls? }\n'
                             'export function authClient(cfg) {\n'
                             '  return createClient(cfg.host, cfg.port, cfg.timeoutMs, cfg.retries, cfg.tls);\n}\n'),
        "services/metrics.js": ('import { createClient } from "../net/client.js";\nimport { createClientPool } from "../net/pool.js";\n\n'
                                'export function metricsClients() {\n'
                                '  const primary = createClient("metrics.internal", 9091, undefined, undefined, true);\n'
                                '  return [primary, ...createClientPool(2, "metrics-replica.internal", 9091)];\n}\n'),
    }
    for k, v in services.items():
        t.fx(k, v)
    index = d('''
        import { authClient } from "./auth.js";
        import { billingClient } from "./billing.js";
        import { geoClient } from "./geo.js";
        import { mailClient } from "./mail.js";
        import { metricsClients } from "./metrics.js";
        import { searchClient } from "./search.js";

        export function allClients(authCfg) {
          return {
            auth: authClient(authCfg),
            billing: billingClient(),
            geo: geoClient(),
            mail: mailClient(),
            metrics: metricsClients(),
            search: searchClient(),
          };
        }
        ''')
    t.fx("services/index.js", index)
    health = d('''
        // node scripts/healthcheck.js <host> [port]
        import { createClient } from "../net/client.js";

        const host = process.argv[2] || "localhost";
        const port = process.argv[3] ? Number(process.argv[3]) : undefined;
        const c = createClient(host, port, 1000, 0);
        console.log(`checking ${c.url("/healthz")}`);
        ''')
    t.fx("scripts/healthcheck.js", health)
    client_test = d('''
        import { test } from "node:test";
        import assert from "node:assert/strict";
        import { createClient } from "../net/client.js";
        import { createClientPool } from "../net/pool.js";
        import { allClients } from "../services/index.js";

        test("defaults", () => {
          const c = createClient("h");
          assert.equal(c.port, 80);
          assert.equal(c.timeoutMs, 5000);
          assert.equal(c.retries, 3);
          assert.equal(c.url("/x"), "http://h:80/x");
        });

        test("tls default port", () => {
          assert.equal(createClient("h", undefined, undefined, undefined, true).port, 443);
        });

        test("host required", () => {
          assert.throws(() => createClient(""), TypeError);
        });

        test("pool", () => {
          const p = createClientPool(3, "h", 81);
          assert.equal(p.length, 3);
          assert.equal(p[2].port, 81);
        });

        test("services build", () => {
          const all = allClients({ host: "auth.internal", tls: true });
          assert.equal(all.auth.port, 443);
          assert.equal(all.metrics.length, 3);
        });
        ''')
    t.fx("test/client.test.js", client_test)
    readme = d('''
        # svcmesh

        Connection descriptors for our internal services.

        ## createClient

        ```js
        import { createClient } from "./net/client.js";

        const c = createClient("billing.internal", 8443, 2000, 0, true);
        ```

        Parameters, in order: `host` (required), `port` (80, or 443 with tls),
        `timeoutMs` (5000; 0 disables the timeout), `retries` (3), `tls` (false).
        Pass `undefined` to skip a parameter.

        `createClientPool(size, host, port)` builds `size` identical clients.

        Tests: `node --test`.
        ''')
    t.fx("README.md", readme)
    js_filler(t, "lib/format", count=4)
    js_filler(t, "lib/retry", count=3)

    new_client = d('''
        // createClient({ host, port, timeoutMs, retries, tls }) describes a connection
        // to an internal service. Omitted (undefined) options take the defaults:
        // port 80 (443 with tls), timeoutMs 5000 (0 = no timeout), retries 3, tls false.
        export function createClient({ host, port, timeoutMs = 5000, retries = 3, tls = false } = {}) {
          if (!host) {
            throw new TypeError("createClient: host is required");
          }
          const secure = tls === true;
          const p = port === undefined ? (secure ? 443 : 80) : port;
          return Object.freeze({
            host,
            port: p,
            timeoutMs,
            retries,
            tls: secure,
            url(path = "/") {
              return `${secure ? "https" : "http"}://${host}:${p}${path}`;
            },
          });
        }
        ''')
    t.ref("net/client.js", new_client)
    t.ref("net/pool.js", sub(pool, ("createClient(host, port)", "createClient({ host, port })")))
    t.ref("services/billing.js", sub(services["services/billing.js"], (
        'createClient("billing.internal", 8443, 2000, 0, true)',
        'createClient({ host: "billing.internal", port: 8443, timeoutMs: 2000, retries: 0, tls: true })')))
    t.ref("services/search.js", sub(services["services/search.js"], (
        'createClient("search.internal", undefined, 750)', 'createClient({ host: "search.internal", timeoutMs: 750 })')))
    t.ref("services/geo.js", sub(services["services/geo.js"], (
        "createClient(GEO_HOST, undefined, undefined, 5)", "createClient({ host: GEO_HOST, retries: 5 })")))
    t.ref("services/mail.js", sub(services["services/mail.js"], (
        'createClient("smtp-relay.internal", 2525, 0)', 'createClient({ host: "smtp-relay.internal", port: 2525, timeoutMs: 0 })')))
    t.ref("services/auth.js", sub(services["services/auth.js"], (
        "createClient(cfg.host, cfg.port, cfg.timeoutMs, cfg.retries, cfg.tls)",
        "createClient({ host: cfg.host, port: cfg.port, timeoutMs: cfg.timeoutMs, retries: cfg.retries, tls: cfg.tls })")))
    t.ref("services/metrics.js", sub(services["services/metrics.js"], (
        'createClient("metrics.internal", 9091, undefined, undefined, true)',
        'createClient({ host: "metrics.internal", port: 9091, tls: true })')))
    t.ref("scripts/healthcheck.js", sub(health, ("createClient(host, port, 1000, 0)",
                                                 "createClient({ host, port, timeoutMs: 1000, retries: 0 })")))
    t.ref("test/client.test.js", sub(client_test,
                                     ('createClient("h");', 'createClient({ host: "h" });'),
                                     ('createClient("h", undefined, undefined, undefined, true)', 'createClient({ host: "h", tls: true })'),
                                     ('createClient("")', 'createClient({ host: "" })')))
    t.ref("README.md", sub(readme, (
        '''const c = createClient("billing.internal", 8443, 2000, 0, true);
        ```

        Parameters, in order: `host` (required), `port` (80, or 443 with tls),
        `timeoutMs` (5000; 0 disables the timeout), `retries` (3), `tls` (false).
        Pass `undefined` to skip a parameter.'''.replace("\n        ", "\n"),
        '''const c = createClient({ host: "billing.internal", port: 8443, timeoutMs: 2000, retries: 0, tls: true });
        ```

        Options: `host` (required), `port` (80, or 443 with tls),
        `timeoutMs` (5000; 0 disables the timeout), `retries` (3), `tls` (false).
        Omitted options take the defaults.'''.replace("\n        ", "\n"))))
    t.answer("createClient now takes a single options object { host, port, timeoutMs, retries, tls } with the same "
             "defaults (0 stays 0 for timeoutMs and retries). Updated pool.js, every service, scripts/healthcheck.js, "
             "the tests and the README; createClientPool keeps its positional signature.\n")
    t.gr("test/zz_hidden.test.js", d('''
        import { test } from "node:test";
        import assert from "node:assert/strict";
        import { createClient } from "../net/client.js";
        import { createClientPool } from "../net/pool.js";
        import { allClients } from "../services/index.js";
        import { authClient } from "../services/auth.js";

        const pick = (c) => ({ host: c.host, port: c.port, timeoutMs: c.timeoutMs, retries: c.retries, tls: c.tls });

        test("hidden: options object and defaults", () => {
          assert.deepEqual(pick(createClient({ host: "h" })), { host: "h", port: 80, timeoutMs: 5000, retries: 3, tls: false });
          assert.equal(createClient({ host: "h", tls: true }).port, 443);
          assert.equal(createClient({ host: "h", retries: 0 }).retries, 0);
          assert.equal(createClient({ host: "h", timeoutMs: 0 }).timeoutMs, 0);
          assert.equal(createClient({ host: "h", port: 8080, tls: true }).url("/x"), "https://h:8080/x");
          assert.equal(createClient({ host: "h", port: undefined, retries: undefined }).retries, 3);
          assert.throws(() => createClient({ port: 1 }));
          assert.throws(() => createClient({ host: "" }));
        });

        test("hidden: pool keeps positional signature", () => {
          const p = createClientPool(2, "r", 9000);
          assert.equal(p.length, 2);
          assert.deepEqual(pick(p[1]), { host: "r", port: 9000, timeoutMs: 5000, retries: 3, tls: false });
        });

        test("hidden: every service is configured as before", () => {
          const all = allClients({ host: "auth.internal" });
          assert.deepEqual(pick(all.billing), { host: "billing.internal", port: 8443, timeoutMs: 2000, retries: 0, tls: true });
          assert.deepEqual(pick(all.search), { host: "search.internal", port: 80, timeoutMs: 750, retries: 3, tls: false });
          assert.deepEqual(pick(all.geo), { host: "geo.internal", port: 80, timeoutMs: 5000, retries: 5, tls: false });
          assert.deepEqual(pick(all.mail), { host: "smtp-relay.internal", port: 2525, timeoutMs: 0, retries: 3, tls: false });
          assert.deepEqual(pick(all.auth), { host: "auth.internal", port: 80, timeoutMs: 5000, retries: 3, tls: false });
          assert.deepEqual(all.metrics.map(pick), [
            { host: "metrics.internal", port: 9091, timeoutMs: 5000, retries: 3, tls: true },
            { host: "metrics-replica.internal", port: 9091, timeoutMs: 5000, retries: 3, tls: false },
            { host: "metrics-replica.internal", port: 9091, timeoutMs: 5000, retries: 3, tls: false },
          ]);
          assert.deepEqual(pick(authClient({ host: "a", port: 1, timeoutMs: 0, retries: 0, tls: true })),
            { host: "a", port: 1, timeoutMs: 0, retries: 0, tls: true });
        });
        '''))
    positional = r"createClient\(\s*(\"[^\"]*\"|'[^']*'|`[^`]*`|[\w.]+)\s*[,)]"
    t.register(
        name="Turn a five-parameter constructor into an options object across all callers (falsy-value traps)",
        category="multi-file", tags=["javascript", "refactor", "multi-file", "api-change"], difficulty="hard",
        prompt=("Change `createClient` in net/client.js to take a single options object "
                "`{ host, port, timeoutMs, retries, tls }` instead of five positional parameters. Defaults and "
                "behaviour stay exactly the same (host is still required). Update every caller in the repository, "
                "the tests and the README. `createClientPool` keeps its current signature. `node --test` must pass."),
        strictDiff=True,
        allowedChangedFiles=["net/*.js", "services/*.js", "scripts/healthcheck.js", "test/*.js", "README.md"],
        forbiddenChangedFiles=["lib/**", "package.json"],
        expectedChangedFiles=["net/client.js", "net/pool.js", "services/billing.js", "services/search.js", "services/geo.js",
                              "services/mail.js", "services/auth.js", "services/metrics.js", "scripts/healthcheck.js",
                              "README.md"],
        verificationCommands=[cmd("node-test", "node", "--test")],
        fileChecks=[{"id": "no-positional-" + f.split("/")[-1].replace(".js", ""), "path": f, "notRegex": [positional],
                     "critical": True}
                    for f in ["net/pool.js", "scripts/healthcheck.js"] + sorted(k for k in services)] + [
            {"id": "readme", "path": "README.md", "regex": [r"createClient\(\{"], "notRegex": [r"createClient\(\""]},
        ],
    )


def build():
    py_geo()
    js_options()
