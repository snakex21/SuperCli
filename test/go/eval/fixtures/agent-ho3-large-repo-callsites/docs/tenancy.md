# Tenancy

Call `billing.tenancy.resolve_tenant(request)` once per request:

    tenant = resolve_tenant(request)

The legacy exporters still use `legacy.tenancy.resolve_tenant(account_number)`.
