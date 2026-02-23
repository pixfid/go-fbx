package tests

import (
	"bytes"
	"fmt"
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

func BenchmarkPackSmall(b *testing.B) {
	benchmarkPackContainer(b, 32, 8<<10, false)
}

func BenchmarkPackMedium(b *testing.B) {
	benchmarkPackContainer(b, 128, 32<<10, false)
}

func BenchmarkPackLarge(b *testing.B) {
	benchmarkPackContainer(b, 256, 64<<10, false)
}

func BenchmarkPackMediumFastUnsafe(b *testing.B) {
	benchmarkPackContainer(b, 128, 32<<10, true)
}

func benchmarkPackContainer(b *testing.B, entryCount int, payloadSize int, fastUnsafe bool) {
	dir := b.TempDir()
	srcPath := filepath.Join(dir, "src.fbx")
	dstPath := filepath.Join(dir, "dst.fbx")

	payload := make([]byte, payloadSize)
	seed := []byte("fb2-benchmark-line-")
	for i := 0; i < payloadSize; i++ {
		payload[i] = seed[i%len(seed)]
	}

	c, err := fbx.Create(srcPath, &fbx.Options{MaxWorkers: 4})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < entryCount; i++ {
		entry := fmt.Sprintf("books/%06d.fb2", i)
		if err := c.Add(entry, bytes.NewReader(payload), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
			_ = c.Close()
			b.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		b.Fatal(err)
	}

	opts := &fbx.PackOptions{
		Codec:      fbx.CodecZstd,
		Level:      3,
		Workers:    4,
		VerifyIn:   false,
		FastUnsafe: fastUnsafe,
	}
	totalBytes := int64(entryCount * payloadSize)
	b.ReportAllocs()
	b.SetBytes(totalBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := fbx.Pack(srcPath, dstPath, opts); err != nil {
			b.Fatal(err)
		}
	}
}
