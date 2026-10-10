package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWebFetchPreservesGenericDocumentArchiveAndDeclaredDownloadLinks(t *testing.T) {
	page := `<!doctype html><html><head><title>Public releases</title></head><body><h1>Release files</h1><a href="../manual.PDF?edition=2026&amp;raw=1">PDF manual</a><a href="//files.example.net/releases/source.zip">ZIP archive</a><a href="../export?signature=a%2Fb%2Bz&amp;name=manual.pdf" download>Signed download</a></body></html>`
	for _, mode := range []string{"media", "text", ""} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			tool := &WebFetch{client: &http.Client{Transport: mediaFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(page)), Request: req}, nil
			})}}
			args, _ := json.Marshal(webFetchArgs{URL: "https://docs.example.org/releases/current", Mode: mode})
			result, err := tool.Spec().Fn(context.Background(), args)
			if err != nil || result.Err != nil || calls != 1 {
				t.Fatalf("page read failed or fetched linked files: calls=%d result=%+v err=%v", calls, result, err)
			}
			for _, want := range []string{
				"https://docs.example.org/manual.PDF?edition=2026&raw=1",
				"https://files.example.net/releases/source.zip",
				"https://docs.example.org/export?signature=a%2Fb%2Bz&name=manual.pdf",
			} {
				if strings.Count(result.Text, want) != 1 {
					t.Errorf("declared link lost, duplicated or changed: %q in %s", want, result.Text)
				}
			}
			if mode == "media" && strings.Contains(result.Text, "Release files") || mode != "media" && !strings.Contains(result.Text, "Release files") {
				t.Fatalf("mode did not preserve its page-text contract: %s", result.Text)
			}
		})
	}
}

func TestFetchedDownloadLinksPrioritizeMetadataDeduplicateAndBoundOutput(t *testing.T) {
	base, _ := url.Parse("https://downloads.example.org/releases/page")
	page := `<html><head><meta property="og:image" content="/preview.gif"><meta property="og:image:secure_url" content="/preview.gif"></head><body><a download="preview.gif" href="/preview.gif">same asset</a><a href="/manual.pdf">PDF</a><a download href="/export?raw=1">export</a><a href="/release.zip">ZIP</a><a href="/fifth.mp3">fifth</a></body></html>`
	got := fetchedMediaURLs(page, "text/html", base, 6000)
	if len(got) > mediaResultBytes || strings.Count(got, "\n- ") != 4 || strings.Count(got, "https://downloads.example.org/preview.gif") != 1 || strings.Contains(got, "fifth.mp3") {
		t.Fatalf("output lost dedup/rank/budget: %s", got)
	}
	if strings.Index(got, "preview.gif") > strings.Index(got, "manual.pdf") {
		t.Fatalf("body links displaced useful page metadata: %s", got)
	}
	for _, maxChars := range []int{10, 100, 250} {
		if got := fetchedMediaURLs(page, "text/html", base, maxChars); len(got) > maxChars {
			t.Fatalf("link output exceeded max_chars=%d: %s", maxChars, got)
		}
	}
}

func TestFetchedDownloadLinksRejectHiddenUnsafeAndNonfileNavigation(t *testing.T) {
	base, _ := url.Parse("https://downloads.example.org/page")
	page := `<html><head><template><meta property="og:image" content="/fake-template-meta.gif"></template></head><body>
<!-- <a download href="/comment.pdf">fake</a> -->
<script>const s = "<a download href='/script.pdf'>fake</a>";</script>
<style>"<a href='/style.zip'>fake</a>"</style>
<textarea><a href="/textarea.zip">fake</a></textarea>
<template><a href="/template.pdf">fake</a><template><a href="/nested.zip">fake</a></template><a href="/outer.zip">fake</a></template>
<noscript><a download href="/noscript.zip">fake</a></noscript>
<nav><a href="/about">About</a><a href="?name=not-a-file.pdf">Search</a><a href="#file.pdf" download>Section</a></nav>
<a href="/view" title=" download ">Not a download attribute</a>
<a href="javascript:alert('x')" download>JS</a><a href="data:application/pdf;base64,AAAA" download>Data</a>
<a href="http://127.0.0.1/private.pdf">Private</a><a href="https://user:password@example.org/private.zip" download>Auth</a>
<a href="/valid/export?signature=literal%2Fbytes&amp;raw=1" DoWnLoAd="document.bin">Actual export</a>
</body></html>`
	got := fetchedMediaURLs(page, "text/html", base, 6000)
	if strings.Count(got, "\n- ") != 1 || !strings.Contains(got, "https://downloads.example.org/valid/export?signature=literal%2Fbytes&raw=1") {
		t.Fatalf("false positives or missing actual download: %s", got)
	}
	for _, forbidden := range []string{"fake-", "comment.pdf", "script.pdf", "style.zip", "textarea", "template", "nested.zip", "outer.zip", "noscript", "not-a-file", "127.0.0.1", "password", "javascript", "base64", "/view"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("hidden/non-file/unsafe link accepted: %q in %s", forbidden, got)
		}
	}
}

func TestFetchedDownloadLinksIncludeExplicitAudioVideoSourcesOnly(t *testing.T) {
	base, _ := url.Parse("https://media.example.org/page")
	page := `<html><body><source src="/not-declared-as-media"><video src="/stream?signature=a%2Bb"><source type="video/mp4" src="/movie.mp4"></video><audio><source src="/sound.ogg"></audio><source type="audio/mpeg" src="/audio-export?raw=1"></body></html>`
	got := fetchedMediaURLs(page, "text/html", base, 6000)
	if strings.Count(got, "\n- ") != 4 || strings.Contains(got, "not-declared-as-media") {
		t.Fatalf("media source scope/count wrong: %s", got)
	}
	for _, want := range []string{"- video: https://media.example.org/stream?signature=a%2Bb", "- video: https://media.example.org/movie.mp4", "- audio: https://media.example.org/sound.ogg", "- audio: https://media.example.org/audio-export?raw=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("explicit media declaration lost: %q in %s", want, got)
		}
	}
}

func TestFetchedDownloadLinksBoundBodyBytesAndTags(t *testing.T) {
	base, _ := url.Parse("https://downloads.example.org/page")
	for _, page := range []string{
		`<html><body>` + strings.Repeat("x", mediaHeadBytes) + `<a href="/too-late.pdf">PDF</a></body></html>`,
		`<html><body>` + strings.Repeat("<span></span>", 270) + `<a download href="/too-many-tags">Export</a></body></html>`,
	} {
		if got := fetchedMediaURLs(page, "text/html", base, 2000); got != "" {
			t.Fatalf("scan exceeded its bounded prefix/tag count: %s", got)
		}
	}
}
