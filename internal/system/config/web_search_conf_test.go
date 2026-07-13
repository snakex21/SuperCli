package config

import "testing"

func TestMergeTomlWebSearchFields(t *testing.T) {
	dst := TomlConfig{WebSearch: WebSearchConf{
		Engine: "brave", APIKey: "global-key", BaseURL: "https://global.example",
	}}
	mergeToml(&dst, TomlConfig{WebSearch: WebSearchConf{
		Engine: "searxng", BaseURL: "https://project.example",
	}})
	if got := dst.WebSearch.Engine; got != "searxng" {
		t.Fatalf("engine = %q", got)
	}
	if got := dst.WebSearch.BaseURL; got != "https://project.example" {
		t.Fatalf("base_url = %q", got)
	}
	if got := dst.WebSearch.APIKey; got != "global-key" {
		t.Fatalf("empty project api_key should inherit global, got %q", got)
	}
}
