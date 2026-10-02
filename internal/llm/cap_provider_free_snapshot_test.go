package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFreeProviderCatalogKeepsIDsWhenOptionalMetadataIsMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"text-free","input_modalities":"text"},{"id":"paid"}]}`))
	}))
	defer srv.Close()
	ids, infos, err := ListFreeProviderCatalog(context.Background(), srv.URL+"/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "text-free" {
		t.Fatalf("IDs changed: %v", ids)
	}
	if len(infos) != 0 {
		t.Fatalf("invalid optional metadata retained: %+v", infos)
	}
}
