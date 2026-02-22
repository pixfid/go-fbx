package fbx

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

func TestPickChunkSize(t *testing.T) {
	opts := PackOptions{ChunkText: 10, ChunkBin: 20}
	if got := pickChunkSize(EntryInfo{Flags: EntryFlagIsText}, opts); got != 10 {
		t.Fatalf("expected text chunk size 10, got %d", got)
	}
	if got := pickChunkSize(EntryInfo{Flags: EntryFlagIsBinary}, opts); got != 20 {
		t.Fatalf("expected bin chunk size 20, got %d", got)
	}
}

func TestPackEmptyInputPath(t *testing.T) {
	if err := Pack("", "", nil); err == nil {
		t.Fatalf("expected empty input path error")
	}
}

func TestPackOutOfPlaceAndLimits(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.fbx")
	out := filepath.Join(dir, "out.fbx")
	c, err := Create(in, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := bytes.Repeat([]byte("a"), 64)
	if err := c.Add("a.fb2", bytes.NewReader(body), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := Pack(in, out, &PackOptions{Codec: CodecZstd, VerifyIn: true}); err != nil {
		t.Fatalf("pack out-of-place: %v", err)
	}
	co, err := Open(out, nil)
	if err != nil {
		t.Fatalf("open packed: %v", err)
	}
	defer co.Close()
	var got bytes.Buffer
	if err := co.Extract("a.fb2", &got); err != nil {
		t.Fatalf("extract packed: %v", err)
	}
	if !bytes.Equal(got.Bytes(), body) {
		t.Fatalf("packed body mismatch")
	}

	err = Pack(in, filepath.Join(dir, "limited.fbx"), &PackOptions{MaxEntrySize: 8, VerifyIn: false})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}
