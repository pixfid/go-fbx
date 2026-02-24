package fbx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/internal/format"
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

func TestPackProgressCallback(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.fbx")
	out := filepath.Join(dir, "out.fbx")
	c, err := Create(in, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader([]byte("a")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add a: %v", err)
	}
	if err := c.Add("b.fb2", bytes.NewReader([]byte("b")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add b: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var (
		startSeen bool
		doneSeen  bool
		lastDone  int
		total     int
	)
	err = Pack(in, out, &PackOptions{
		Codec:    CodecStore,
		VerifyIn: false,
		Progress: func(ev PackProgress) {
			switch ev.Phase {
			case "start":
				startSeen = true
				total = ev.EntriesTotal
			case "entry_done":
				lastDone = ev.EntriesDone
			case "done":
				doneSeen = true
				lastDone = ev.EntriesDone
				total = ev.EntriesTotal
			}
		},
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !startSeen || !doneSeen {
		t.Fatalf("expected start and done progress events")
	}
	if total != 2 || lastDone != 2 {
		t.Fatalf("unexpected progress totals: total=%d done=%d", total, lastDone)
	}
}

func TestPackClearMeta(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.fbx")
	out := filepath.Join(dir, "out.fbx")
	body := bytes.Repeat([]byte("meta-body-"), 64)
	meta := []byte(`{"id":123,"source":"zip"}`)

	c, err := Create(in, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader(body), meta, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := Pack(in, out, &PackOptions{Codec: CodecStore, VerifyIn: true, ClearMeta: true}); err != nil {
		t.Fatalf("pack clear-meta: %v", err)
	}
	co, err := Open(out, nil)
	if err != nil {
		t.Fatalf("open packed: %v", err)
	}
	defer co.Close()

	info, err := co.Stat("a.fb2")
	if err != nil {
		t.Fatalf("stat packed entry: %v", err)
	}
	if len(info.Meta) != 0 {
		t.Fatalf("expected empty metadata, got %q", string(info.Meta))
	}
	var got bytes.Buffer
	if err := co.Extract("a.fb2", &got); err != nil {
		t.Fatalf("extract packed: %v", err)
	}
	if !bytes.Equal(got.Bytes(), body) {
		t.Fatalf("packed body mismatch")
	}
}

func TestPackFastUnsafeBypassesChunkCRC(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.fbx")
	outStrict := filepath.Join(dir, "strict.fbx")
	outFast := filepath.Join(dir, "fast.fbx")
	orig := bytes.Repeat([]byte("payload-"), 256)

	c, err := Create(in, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader(orig), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := corruptFirstChunkPayloadByte(in); err != nil {
		t.Fatalf("corrupt chunk: %v", err)
	}
	src, err := Open(in, nil)
	if err != nil {
		t.Fatalf("open corrupted source: %v", err)
	}
	var probe bytes.Buffer
	srcErr := src.Extract("a.fb2", &probe)
	_ = src.Close()
	if !errors.Is(srcErr, ErrCRCMismatch) {
		t.Fatalf("expected corrupted source extract to fail with ErrCRCMismatch, got %v", srcErr)
	}

	err = Pack(in, outStrict, &PackOptions{Codec: CodecStore, VerifyIn: true})
	if !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("expected ErrCRCMismatch with input verification, got %v", err)
	}

	if err := Pack(in, outFast, &PackOptions{Codec: CodecStore, VerifyIn: false, FastUnsafe: true}); err != nil {
		t.Fatalf("pack fast mode: %v", err)
	}
	co, err := Open(outFast, nil)
	if err != nil {
		t.Fatalf("open packed fast output: %v", err)
	}
	defer co.Close()
	var got bytes.Buffer
	if err := co.Extract("a.fb2", &got); err != nil {
		t.Fatalf("extract packed fast output: %v", err)
	}
	if bytes.Equal(got.Bytes(), orig) {
		t.Fatalf("expected fast mode to pack corrupted payload bytes")
	}
}

func corruptFirstChunkPayloadByte(containerPath string) error {
	f, err := os.OpenFile(containerPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		return err
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		return err
	}
	dirBlob := make([]byte, h.DirSize)
	if _, err := f.ReadAt(dirBlob, int64(h.DirOffset)); err != nil {
		return err
	}
	dir, err := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize)
	if err != nil {
		return err
	}
	if len(dir.Entries) == 0 || len(dir.Entries[0].Chunks) == 0 {
		return ErrInvalidFormat
	}
	first := dir.Entries[0].Chunks[0]
	head := make([]byte, 16)
	if _, err := f.ReadAt(head, int64(first.ChunkOffset)); err != nil {
		return err
	}
	compSize := binary.LittleEndian.Uint32(head[8:12])
	if compSize == 0 {
		return ErrInvalidFormat
	}
	payloadOff := int64(first.ChunkOffset) + 16
	b := []byte{0}
	if _, err := f.ReadAt(b, payloadOff); err != nil {
		return err
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, payloadOff); err != nil {
		return err
	}
	return nil
}
