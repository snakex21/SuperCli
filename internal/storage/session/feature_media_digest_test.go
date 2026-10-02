package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestExternalizeImageDigestIdentity(t *testing.T) {
	data := []byte("image digest fixture")
	sum := sha256.Sum256(data)
	root := t.TempDir()
	store := &Store{root: root}
	writer := NewWriter(store, "identity-fixture")
	for _, mime := range []string{" IMAGE/PNG ", "image/jpeg", "image/jpg", "image/webp", "image/gif", "image/unknown"} {
		t.Run(mime, func(t *testing.T) {
			normalized := strings.TrimSpace(strings.ToLower(mime))
			want := llm.ImageRef{MediaType: normalized, Path: filepath.Join(store.sessionMediaDir("identity-fixture"), fmt.Sprintf("%x%s", sum[:], imageExtension(normalized))), ID: fmt.Sprintf("img_%x", sum[:6])}
			for i := 0; i < 2; i++ {
				got, err := writer.ExternalizeImage(context.Background(), mime, data)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("ref=%+v, want %+v", got, want)
				}
				saved, err := os.ReadFile(got.Path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(saved, data) {
					t.Fatalf("bytes changed: %q", saved)
				}
			}
		})
	}
}

func TestExternalizeImageErrorsPreserveEmptyRef(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	invalidRoot := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(invalidRoot, []byte("block mkdir"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		writer    *Writer
		ctx       context.Context
		mime      string
		data      []byte
		errorText string
	}{
		{"nil writer", nil, context.Background(), "image/png", []byte("x"), "nil writer/store"},
		{"nil store", &Writer{}, context.Background(), "image/png", []byte("x"), "nil writer/store"},
		{"canceled", NewWriter(&Store{root: t.TempDir()}, "fixture"), ctx, "image/png", []byte("x"), "context canceled"},
		{"empty mime", NewWriter(&Store{root: t.TempDir()}, "fixture"), context.Background(), " ", []byte("x"), "media type and data are required"},
		{"empty data", NewWriter(&Store{root: t.TempDir()}, "fixture"), context.Background(), "image/png", nil, "media type and data are required"},
		{"empty root", NewWriter(&Store{}, "fixture"), context.Background(), "image/png", []byte("x"), "store root is empty"},
		{"empty session", NewWriter(&Store{root: t.TempDir()}, " "), context.Background(), "image/png", []byte("x"), "session id is empty"},
		{"mkdir blocked", NewWriter(&Store{root: invalidRoot}, "fixture"), context.Background(), "image/png", []byte("x"), "storeSessionImage mkdir:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.writer.ExternalizeImage(tc.ctx, tc.mime, tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("error=%v, want %q", err, tc.errorText)
			}
			if !reflect.DeepEqual(got, llm.ImageRef{}) {
				t.Fatalf("failed ref=%+v", got)
			}
		})
	}
}

func BenchmarkExternalizeImageDigest(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20} {
		b.Run(fmt.Sprintf("dedup-%dMiB", size>>20), func(b *testing.B) {
			store := &Store{root: b.TempDir()}
			writer := NewWriter(store, "image-benchmark")
			data := bytes.Repeat([]byte{0x8a}, size)
			first, err := writer.ExternalizeImage(context.Background(), "image/png", data)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := writer.ExternalizeImage(context.Background(), "image/png", data)
				if err != nil || got != first {
					b.Fatalf("ref changed: %+v / %v", got, err)
				}
			}
		})
	}
}

func TestLegacyImageDigestPreservesOtherParts(t *testing.T) {
	store := &Store{root: t.TempDir()}
	raw := []byte("legacy image digest fixture")
	sum := sha256.Sum256(raw)
	original := llm.ImageRef{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(raw), Name: "legacy.png", Active: true}
	unchanged := llm.ImageRef{MediaType: "image/png", Data: "malformed base64"}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "inspect", Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "keep text"}, {Type: llm.PartTypeImage, Image: &original}, {Type: llm.PartTypeImage, Image: &unchanged}}}, {Role: llm.RoleAssistant, Content: "next answer"}}
	got := store.externalizeModelImages("legacy-digest-fixture", msgs)
	wantImage := llm.ImageRef{MediaType: "image/png", Path: filepath.Join(store.sessionMediaDir("legacy-digest-fixture"), fmt.Sprintf("%x.png", sum[:])), ID: fmt.Sprintf("img_%x", sum[:6]), Name: "legacy.png"}
	if !reflect.DeepEqual(*got[0].Parts[1].Image, wantImage) {
		t.Fatalf("legacy ref=%+v, want %+v", got[0].Parts[1].Image, wantImage)
	}
	if got[0].Parts[0].Text != "keep text" || got[1].Content != "next answer" || got[0].Parts[2].Image != &unchanged || !reflect.DeepEqual(*got[0].Parts[2].Image, unchanged) {
		t.Fatal("nonconverted content changed")
	}
	if original.Data == "" || !original.Active {
		t.Fatal("original image alias was mutated")
	}
}
