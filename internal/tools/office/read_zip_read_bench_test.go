package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkReadZip_ReadAcrossExplicitParts(b *testing.B) {
	dir := b.TempDir()
	for part := 1; part <= 2; part++ {
		file, err := os.Create(filepath.Join(dir, fmt.Sprintf("part%02d.zip", part)))
		if err != nil {
			b.Fatal(err)
		}
		writer := zip.NewWriter(file)
		for i := 0; i < 128; i++ {
			entry, err := writer.Create(fmt.Sprintf("Bundle/models/model_%03d.glb", i))
			if err != nil {
				b.Fatal(err)
			}
			if _, err := entry.Write([]byte("unused binary fixture")); err != nil {
				b.Fatal(err)
			}
		}
		if part == 2 {
			entry, err := writer.Create("Bundle/pliki/assets/travelers_v3_manifest.json")
			if err != nil {
				b.Fatal(err)
			}
			if _, err := entry.Write([]byte(`{"fixture":true,"models":128}`)); err != nil {
				b.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			b.Fatal(err)
		}
		if err := file.Close(); err != nil {
			b.Fatal(err)
		}
	}
	tool := NewReadZip(dir, 0)
	args := json.RawMessage(`{"paths":["part01.zip","part02.zip"],"action":"read","pattern":"travelers_v3_manifest.json"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := tool.Execute(context.Background(), args)
		if err != nil || res.Err != nil {
			b.Fatalf("result=%+v err=%v", res, err)
		}
	}
}
