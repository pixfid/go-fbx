package tests

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
)

func TestPerfGuardEncodeChunkZstdAllocs(t *testing.T) {
	payload := bytes.Repeat([]byte("compressible-text-"), (1<<20)/18)
	// Warm-up to initialize cached encoders/decoders and avoid first-call noise.
	if _, _, err := format.EncodeChunkRecord(payload, format.CodecZstd, 1); err != nil {
		t.Fatalf("warm-up encode: %v", err)
	}
	allocs := testing.AllocsPerRun(40, func() {
		if _, _, err := format.EncodeChunkRecord(payload, format.CodecZstd, 1); err != nil {
			t.Fatalf("encode: %v", err)
		}
	})
	if allocs > 12 {
		t.Fatalf("zstd encode alloc regression: got %.2f allocs/op, want <= 12", allocs)
	}
}

func TestPerfGuardContainerAddExtractAllocs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guard.fbx")
	payload := bytes.Repeat([]byte("bench-data-"), (1<<20)/11)

	c, err := fbx.Create(path, &fbx.Options{MaxWorkers: 4})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer c.Close()

	allocs := testing.AllocsPerRun(20, func() {
		entry := "bench/item.fb2"
		if err := c.Upsert(entry, bytes.NewReader(payload), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		var out bytes.Buffer
		if err := c.Extract(entry, &out); err != nil {
			t.Fatalf("extract: %v", err)
		}
		if out.Len() != len(payload) {
			t.Fatalf("size mismatch: got=%d want=%d", out.Len(), len(payload))
		}
	})
	if allocs > 180 {
		t.Fatalf("add+extract alloc regression: got %.2f allocs/op, want <= 180", allocs)
	}
}
