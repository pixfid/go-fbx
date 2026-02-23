package tests

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
)

func BenchmarkEncodeChunkStore1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("A"), 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		if _, _, err := format.EncodeChunkRecord(payload, format.CodecStore, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeChunkZstd1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("compressible-text-"), (1<<20)/18)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		if _, _, err := format.EncodeChunkRecord(payload, format.CodecZstd, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeChunkLZ41MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("compressible-text-"), (1<<20)/18)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		if _, _, err := format.EncodeChunkRecord(payload, format.CodecLZ4, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkContainerAddExtract1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("bench-data-"), (1<<20)/11)
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.fbx")
	c, err := fbx.Create(path, &fbx.Options{MaxWorkers: 4})
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		entry := "bench/item.fb2"
		if err := c.Upsert(entry, bytes.NewReader(payload), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd}); err != nil {
			b.Fatal(err)
		}
		var out bytes.Buffer
		if err := c.Extract(entry, &out); err != nil {
			b.Fatal(err)
		}
		if out.Len() != len(payload) {
			b.Fatalf("size mismatch: got=%d want=%d", out.Len(), len(payload))
		}
	}
}
