# blobhub

Blob storage behind one interface, `storage.Store` (storage/store.go), with
implementations in storage/* and consumers in service/* and cmd/.
`cache.Cache` is a separate, infallible thumbnail cache.

`go test ./...`
