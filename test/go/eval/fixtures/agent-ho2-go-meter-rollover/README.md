# meterd

Monthly electricity billing from head-end exports. Packages: ingest (CSV),
meter (model registry), readings (consumption), usage (per customer),
tariff (prices), billing (invoices). `go test ./...` runs everything against
the March 2027 exports in testdata/march.
