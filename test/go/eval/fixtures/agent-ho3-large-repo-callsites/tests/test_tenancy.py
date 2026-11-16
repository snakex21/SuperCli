import unittest
from types import SimpleNamespace

from billing.cache import TenantCache
from billing import tenancy
from billing.tenancy import resolve_tenant


class TenancyTest(unittest.TestCase):
    def setUp(self):
        tenancy._cache = TenantCache()
        tenancy._cache.put("acme", "ACME")

    def test_header(self):
        req = SimpleNamespace(headers={"X-Tenant": "acme"}, host="x.example.com")
        self.assertEqual(resolve_tenant(req), "ACME")

    def test_subdomain(self):
        req = SimpleNamespace(headers={}, host="acme.example.com")
        self.assertEqual(tenancy.resolve_tenant(req), "ACME")
