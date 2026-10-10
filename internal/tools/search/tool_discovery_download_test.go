package search

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/tools/web"
)

func TestWebDownloadDiscoveryFromAssetAndPolishQueries(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		reg := NewRegistry()
		reg.MustRegister(web.NewWebDownload(t.TempDir()).Spec())
		reg.MustRegister(web.NewWebFetch().Spec())
		var idx *Index
		if indexed {
			var err error
			idx, err = NewInMemoryIndex()
			if err != nil {
				t.Fatal(err)
			}
			defer idx.Close()
		}
		searcher := NewToolSearcher(reg, idx)
		if idx != nil {
			if err := searcher.RebuildIndex(); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range []string{"download game textures ZIP", "downloading assets", "downloaded image", "downloads fonts", "pobierz plik", "pobieranie zasobow", "pobranie tekstury", "web_download"} {
			args, _ := json.Marshal(map[string]any{"query": query, "limit": 1})
			result, err := searcher.Spec().Fn(context.Background(), args)
			var got discoveryResponse
			parseErr := json.Unmarshal([]byte(result.Text), &got)
			if err != nil || result.Err != nil || parseErr != nil || len(got.Matches) != 1 || got.Matches[0].Name != "web_download" || !reg.IsVisible("web_download") {
				t.Fatalf("download discovery indexed=%v query=%q: %+v %v", indexed, query, result, err)
			}
		}
		for _, query := range []string{"read web page", "read a web page about downloading assets", "read download instructions", "fetch web page explaining downloads", "web_fetch"} {
			args, _ := json.Marshal(map[string]any{"query": query, "limit": 1})
			result, err := searcher.Spec().Fn(context.Background(), args)
			var got discoveryResponse
			parseErr := json.Unmarshal([]byte(result.Text), &got)
			if err != nil || result.Err != nil || parseErr != nil || len(got.Matches) != 1 || got.Matches[0].Name != "web_fetch" {
				t.Fatalf("page read discovery indexed=%v query=%q: %+v %v", indexed, query, result, err)
			}
		}
	}
}

func TestDownloadIntentNeverOverridesExactToolListsOrUnavailableTools(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(web.NewWebFetch().Spec())
	if hit := downloadIntentHit(reg, "downloading assets"); hit != nil {
		t.Fatalf("unregistered download tool exposed: %+v", hit)
	}
	reg.MustRegister(web.NewWebDownload(t.TempDir()).Spec())
	for _, query := range []string{"read image download instructions", "download email attachment file", "download and unzip archive", "web page about downloading assets"} {
		if hit := downloadIntentHit(reg, query); hit != nil {
			t.Errorf("ordinary page/mail/archive query overridden: %q %+v", query, hit)
		}
	}
	searcher := NewToolSearcher(reg, nil)
	result, err := searcher.Spec().Fn(context.Background(), []byte(`{"query":"web_fetch web_download","limit":2}`))
	var got discoveryResponse
	parseErr := json.Unmarshal([]byte(result.Text), &got)
	if err != nil || result.Err != nil || parseErr != nil || len(got.Matches) != 2 || got.Matches[0].Name != "web_fetch" || got.Matches[1].Name != "web_download" {
		t.Fatalf("exact tool list lost priority/order: %+v %v", result, err)
	}
}
