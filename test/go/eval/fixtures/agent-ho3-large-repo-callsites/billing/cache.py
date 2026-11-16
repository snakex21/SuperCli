class TenantCache:
    def __init__(self):
        self._data = {}

    def get(self, key):
        return self._data[key]

    def put(self, key, tenant):
        self._data[key] = tenant
