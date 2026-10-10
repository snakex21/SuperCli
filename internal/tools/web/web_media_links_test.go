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

func TestWebFetchMediaModeReturnsDeclaredURLsWithoutPageProse(t *testing.T) {
	page := `<!doctype html><html><head><title>Two assets</title><meta property="og:image" content="/assets/first.gif"><meta name="twitter:image" content="https://cdn.example.net/second.gif?raw=1&amp;size=2"><meta property="og:video" content="/assets/preview.mp4"></head><body>unrelated page prose ` + strings.Repeat("body ", 50000) + `</body></html>`
	for _, mode := range []string{"media", "text", ""} {
		t.Run(mode, func(t *testing.T) {
			stream := &countingMediaBody{Reader: strings.NewReader(page)}
			calls := 0
			tool := &WebFetch{client: &http.Client{Transport: mediaFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: stream, Request: r}, nil
			})}}
			args, _ := json.Marshal(webFetchArgs{URL: "https://example.org/gallery/page", Mode: mode, MaxChars: 6000})
			result, err := tool.execute(context.Background(), args)
			if err != nil || result.Err != nil || calls != 1 || !stream.closed {
				t.Fatalf("fetch failed or made extra request: calls=%d closed=%v result=%+v err=%v", calls, stream.closed, result, err)
			}
			for _, required := range []string{"Title: Two assets", "https://example.org/assets/first.gif", "https://cdn.example.net/second.gif?raw=1&size=2", "https://example.org/assets/preview.mp4"} {
				if !strings.Contains(result.Text, required) {
					t.Fatalf("missing %q: %s", required, result.Text)
				}
			}
			if mode == "media" {
				if len(result.Text) > 700 || strings.Contains(result.Text, "unrelated page prose") || stream.read > mediaHeadBytes+4096 || !strings.Contains(result.Text, "web_download") {
					t.Fatalf("media mode retained body/read too far: bytes=%d output=%s", stream.read, result.Text)
				}
			} else if !strings.Contains(result.Text, "unrelated page prose") || stream.read != len(page) {
				t.Fatalf("ordinary page read lost content: bytes=%d output=%s", stream.read, result.Text)
			}
		})
	}
}

func TestWebFetchMediaModeNoMetadataDoesNotReturnPageOrGuessURL(t *testing.T) {
	tool := &WebFetch{client: &http.Client{Transport: mediaFixtureTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(`<html><head><title>Example</title></head><body>https://cdn.example.net/not-declared.gif<script>"https://cdn.example.net/script.gif"</script></body></html>`)), Request: r}, nil
	})}}
	result, err := tool.execute(context.Background(), []byte(`{"url":"https://example.org/page","mode":"media"}`))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "No declared media URLs") || strings.Contains(result.Text, "not-declared") || strings.Contains(result.Text, "script.gif") {
		t.Fatalf("media mode guessed or retained prose: %+v %v", result, err)
	}
}

func TestWebFetchMediaHandoffUsesOnlyDeclaredCandidates(t *testing.T) {
	pageURL := "https://gallery.arbitrary.example/item?ticket=a%2Bb&part=1&part=2"
	assetURL := "https://gallery.arbitrary.example/files/object?id=42&signature=a%2Bb%2Fc%3D&part=1&part=2"
	for _, fixture := range []struct {
		name, body string
		declared   bool
	}{
		{"declared-signed-download", `<html><head><title>Asset</title></head><body><a download href="/files/object?id=42&amp;signature=a%2Bb%2Fc%3D&amp;part=1&amp;part=2">Save</a></body></html>`, true},
		{"undeclared-prose-and-script", `<html><head><title>Asset</title></head><body>Verified file: ` + assetURL + `<script>const asset = "` + assetURL + `";</script></body></html>`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			requests := 0
			tool := &WebFetch{client: &http.Client{Transport: mediaFixtureTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.String() != pageURL {
					t.Fatalf("page URL changed or candidate fetched: %s", r.URL.String())
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(fixture.body)), Request: r}, nil
			})}}
			raw, _ := json.Marshal(webFetchArgs{URL: pageURL, Mode: "media"})
			result, err := tool.execute(context.Background(), raw)
			if err != nil || result.Err != nil || requests != 1 {
				t.Fatalf("fetch failed or performed additional network action: result=%+v err=%v requests=%d", result, err, requests)
			}
			if !strings.Contains(result.Text, pageURL) || !strings.Contains(result.Text, "Title: Asset") || strings.Contains(result.Text, "Verified file") || strings.Contains(result.Text, "SHA256") {
				t.Fatalf("result lost page evidence or promoted candidates to verified files: %s", result.Text)
			}
			if fixture.declared {
				for _, evidence := range []string{assetURL, "Page-declared file candidates", "web_download", "Another lookup is unnecessary", "web_download checks the response", "web_fetch(mode=text)"} {
					if !strings.Contains(result.Text, evidence) {
						t.Fatalf("declared candidate handoff omits %q: %s", evidence, result.Text)
					}
				}
			} else if strings.Contains(result.Text, assetURL) || strings.Contains(result.Text, "web_download") || !strings.Contains(result.Text, "No declared media URLs") {
				t.Fatalf("undeclared body text became a download candidate: %s", result.Text)
			}
		})
	}
}

func TestWebFetchMediaModeContractAndInvalidModeBeforeNetwork(t *testing.T) {
	tool := &WebFetch{client: &http.Client{Transport: mediaFixtureTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid mode reached network")
		return nil, nil
	})}}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(tool.Spec().Schema), &schema); err != nil || strings.Join(schema.Properties["mode"].Enum, ",") != "text,media" || !strings.Contains(tool.Spec().Description, "mode=media") {
		t.Fatalf("media mode not exposed in exact contract: %+v %v", schema, err)
	}
	result, err := tool.execute(context.Background(), []byte(`{"url":"https://example.org/page","mode":"html"}`))
	if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "mode") {
		t.Fatalf("invalid mode accepted: %+v %v", result, err)
	}
}

type countingMediaBody struct {
	*strings.Reader
	read   int
	closed bool
}

func (r *countingMediaBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}
func (r *countingMediaBody) Close() error { r.closed = true; return nil }

func TestFetchedMediaURLsPreservesDeclaredAssetURLs(t *testing.T) {
	base, _ := url.Parse("https://example.org/gallery/item")
	page := `<html><head><meta content='//cdn.example.org/hello.gif?x=1&amp;y=2' property='og:image'><meta name="twitter:image" content="//cdn.example.org/hello.gif?x=1&amp;y=2"><meta property="og:video" content="/hello.mp4"><meta property="og:video:secure_url" content="/hello.mp4"></head><body>Text</body></html>`
	got := fetchedMediaURLs(page, "text/html", base, 6000)
	if strings.Count(got, "https://cdn.example.org/hello.gif?x=1&y=2") != 1 || strings.Count(got, "https://example.org/hello.mp4") != 1 {
		t.Fatalf("declared URLs lost/duplicated: %s", got)
	}
	if got := fetchedMediaURLs(page, "text/plain", base, 6000); got != "" {
		t.Fatalf("plain text interpreted as HTML: %s", got)
	}
}

func TestFetchedMediaURLsRejectsUnsafeAndBoundsMetadata(t *testing.T) {
	base, _ := url.Parse("https://example.org/page")
	page := `<html><head><meta property="og:image" content="javascript:alert(1)"><meta property="og:image" content="data:image/png;base64,xx"><meta property="og:video" content="http://127.0.0.1/a.mp4"><meta property="og:video" content="https://user:secret@example.org/private.mp4"><meta property="og:image" content="https://example.org/valid.gif"></head></html>`
	got := fetchedMediaURLs(page, "text/html", base, 2000)
	if !strings.Contains(got, "https://example.org/valid.gif") || strings.Contains(got, "secret") || strings.Contains(got, "127.0.0.1") || strings.Contains(got, "javascript") || strings.Contains(got, "base64") {
		t.Fatalf("unsafe metadata: %s", got)
	}
	if got := fetchedMediaURLs(page, "text/html", base, 10); got != "" {
		t.Fatalf("metadata exceeded small budget: %s", got)
	}
	if got := fetchedMediaURLs("<html>"+strings.Repeat("x", mediaHeadBytes)+page, "text/html", base, 2000); got != "" {
		t.Fatal("metadata scan exceeded bounded prefix")
	}
	large := `<html><head><meta property="og:image" content="https://example.org/` + strings.Repeat("x", mediaURLBytes) + `.gif"></head></html>`
	if got := fetchedMediaURLs(large, "text/html", base, 2000); got != "" {
		t.Fatal("oversized URL accepted")
	}
}

func TestWebFetchIncludesTenorStyleMediaWithoutAnotherFetch(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: mediaFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(`<!doctype html><html><head><title>Hello GIF</title><meta class="dynamic" name="twitter:image" content="https://media1.tenor.com/m/K5yZZDVaCFkAAAAC/hello-waving.gif"><meta property="og:image" content="https://media1.tenor.com/m/K5yZZDVaCFkAAAAC/hello-waving.gif"><meta property="og:video" content="https://media.tenor.com/K5yZZDVaCFkAAAPo/hello-waving.mp4"></head><body>Hello waving</body></html>`)), Request: r}, nil
	})}
	tool := &WebFetch{client: client}
	result, err := tool.execute(context.Background(), []byte(`{"url":"https://tenor.com/view/example","max_chars":6000}`))
	if err != nil || result.Err != nil || calls != 1 {
		t.Fatalf("fetch failed/extra network: calls=%d err=%v result=%v", calls, err, result.Err)
	}
	if !strings.Contains(result.Text, "Title: Hello GIF") || !strings.Contains(result.Text, "Hello waving") || strings.Count(result.Text, "https://media1.tenor.com/m/K5yZZDVaCFkAAAAC/hello-waving.gif") != 1 {
		t.Fatalf("lost page/media: %s", result.Text)
	}
}

type mediaFixtureTransport func(*http.Request) (*http.Response, error)

func (f mediaFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func BenchmarkFetchedMediaMetadata(b *testing.B) {
	base, _ := url.Parse("https://example.org/page")
	ordinary := "<html><head><title>Ordinary page</title></head><body>" + strings.Repeat("text ", 24000) + "</body></html>"
	for _, fixture := range []struct{ name, body, ct string }{
		{"ordinary-no-media", ordinary, "text/html"},
		{"ordinary-unknown-type", ordinary + strings.Repeat("X", 2<<20), ""},
		{"declared-media", `<html><head><title>Asset page</title><meta property="og:image" content="https://cdn.example.org/hello.gif"><meta property="og:video" content="https://cdn.example.org/hello.mp4"></head><body>` + strings.Repeat("text ", 24000) + "</body></html>", "text/html"},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got := fetchedMediaURLs(fixture.body, fixture.ct, base, 6000)
				if fixture.name == "declared-media" && got == "" {
					b.Fatal("lost media")
				}
			}
		})
	}
}

func BenchmarkFetchedPageTextVersusMediaOnly(b *testing.B) {
	base, _ := url.Parse("https://example.org/gallery/page")
	page := `<html><head><title>Asset gallery</title><meta property="og:image" content="https://cdn.example.org/first.gif"><meta property="og:image" content="https://cdn.example.org/second.gif"></head><body>` + strings.Repeat("Unrelated gallery prose and navigation. ", 6000) + `</body></html>`
	b.Run("text", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			title, content := formatFetched(page, "text/html")
			if title == "" || content == "" {
				b.Fatal("lost ordinary page content")
			}
		}
	})
	b.Run("media", func(b *testing.B) {
		prefix := page[:mediaHeadBytes]
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			media := fetchedMediaURLs(prefix, "text/html", base, 2000)
			if result := formatFetchedMedia(prefix, "text/html", base, media); !strings.Contains(result, "second.gif") {
				b.Fatal("lost declared asset")
			}
		}
	})
}

func TestFetchedMediaURLsIgnoresCommentAndRawTextTags(t *testing.T) {
	base, _ := url.Parse("https://example.org/page")
	real := "https://example.org/real.gif"
	for _, noise := range []string{
		"<!-- </head><meta property='og:image' content='https://example.org/fake.gif'> -->",
		"<script>const s = \"</head><meta property='og:image' content='https://example.org/fake.gif'>\";</script>",
		"<STYLE>/* </head><meta property='og:image' content='https://example.org/fake.gif'> */</STYLE>",
		"<title>&lt;Hello <meta property='og:image' content='https://example.org/fake.gif'></head></title>",
		"<!-- " + strings.Repeat("<meta property='og:image' content='https://example.org/fake.gif'>", 5) + " -->",
	} {
		page := "<!DOCTYPE HTML><HTML><HEAD>" + noise + "<META PROPERTY='og:image' CONTENT='" + real + "'></HEAD><BODY><meta property='og:image' content='https://example.org/body.gif'></BODY></HTML>"
		for _, ct := range []string{"text/html", "", "application/octet-stream"} {
			got := fetchedMediaURLs(page, ct, base, 2000)
			if !strings.Contains(got, real) || strings.Contains(got, "fake.gif") || strings.Contains(got, "body.gif") {
				t.Fatalf("invalid metadata from %q, type %q: %s", noise, ct, got)
			}
		}
	}
	for _, page := range []string{"<html><head><!-- <meta property='og:image' content='" + real + "'>", "<html><head><script> <meta property='og:image' content='" + real + "'>"} {
		if got := fetchedMediaURLs(page, "text/html", base, 2000); got != "" {
			t.Fatalf("unclosed comment/script leaked: %s", got)
		}
	}
}

func TestFetchedMediaURLsBoundsUnknownContentType(t *testing.T) {
	base, _ := url.Parse("https://example.org/page")
	page := "<!DOCTYPE HTML><HTML><HEAD><title>Ordinary</title></HEAD><BODY>" + strings.Repeat("X", 2<<20) + "<meta property='og:image' content='https://example.org/body.gif'></BODY></HTML>"
	if got := fetchedMediaURLs(page, "", base, 2000); got != "" {
		t.Fatalf("body scanned: %s", got)
	}
	if n := testing.AllocsPerRun(10, func() { fetchedMediaURLs(page, "", base, 2000) }); n != 0 {
		t.Fatalf("ordinary HTML allocated %.1f times", n)
	}
}

func TestFetchedMediaURLsScriptEscapedAndNearMatchingNames(t *testing.T) {
	base, _ := url.Parse("https://example.org/page")
	fake := "<meta property='og:image' content='https://example.org/fake.gif'>"
	real := "<meta property='og:image' content='https://example.org/real.gif'>"
	for _, raw := range []string{
		"<!--<script></script>" + fake,
		"<!--<SCRIPT></script " + fake,
		"<!--<script>--><scriptx>" + fake,
		strings.Repeat("</scriptx ", 6000) + fake,
		"<!--" + fake,
	} {
		page := "<html><head><script>" + raw + "</script>" + real + "</head></html>"
		got := fetchedMediaURLs(page, "text/html", base, 2000)
		if strings.Contains(got, "fake.gif") || !strings.Contains(got, "real.gif") {
			t.Fatalf("raw script leaked or hid real metadata: %s", got)
		}
	}
}

func BenchmarkFetchedMediaNearMatchScript(b *testing.B) {
	base, _ := url.Parse("https://example.org/page")
	page := "<html><head><script>" + strings.Repeat("</scriptx ", 6000) + "</script><meta property='og:image' content='https://example.org/real.gif'></head></html>"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := fetchedMediaURLs(page, "text/html", base, 2000); !strings.Contains(got, "real.gif") {
			b.Fatal("lost real metadata")
		}
	}
}

func TestFetchedMediaURLsSkipsInertDeclarations(t *testing.T) {
	base, _ := url.Parse("https://example.org/page")
	fake := "<meta property='og:image' content='https://example.org/fake.gif'>"
	real := "<meta property='og:image' content='https://example.org/real.gif'>"
	for _, noise := range []string{
		"<![CDATA[" + fake + "]]>",
		"<?processing " + fake + "?>",
		"<!unknown " + fake + ">",
		"<!DOCTYPE html PUBLIC \"" + fake + "\">",
	} {
		got := fetchedMediaURLs("<html><head>"+noise+real+"</head></html>", "text/html", base, 2000)
		if strings.Contains(got, "fake.gif") || !strings.Contains(got, "real.gif") {
			t.Fatalf("inert declaration leaked: %s", got)
		}
	}
}
