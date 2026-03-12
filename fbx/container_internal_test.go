package fbx

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pixfid/go-fbx/internal/format"
	"github.com/pixfid/go-fbx/internal/pathutil"
)

// readAll is a test helper; it lives here (not in production code).
func readAll(r io.Reader) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	_, err := io.Copy(buf, r)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestMergeOptionsAndMapErr(t *testing.T) {
	base := defaultOptions()

	// Passing a partial Options must NOT silently disable default-true booleans.
	partial := Options{MaxWorkers: 2}
	out := mergeOptions(base, partial)
	if !out.DetectText || !out.StoreIfAlreadyCompressed || !out.SyncOnCommit || !out.StrictVerify {
		t.Fatalf("partial Options must preserve default-true booleans: %+v", out)
	}

	// Explicit opt-out via No* flags must work.
	in := Options{
		ChunkSizeText:              128,
		ChunkSizeBin:               256,
		DefaultCodec:               CodecZstd,
		DefaultLevel:               3,
		MaxWorkers:                 2,
		MaxEntrySize:               100,
		MaxChunkSize:               10,
		NoDetectText:               true,
		NoStoreIfAlreadyCompressed: true,
		NoSyncOnCommit:             true,
		NoStrictVerify:             true,
	}
	out = mergeOptions(base, in)
	if out.ChunkSizeText != 128 || out.ChunkSizeBin != 256 || out.DefaultCodec != CodecZstd {
		t.Fatalf("unexpected merged chunk/codec options: %+v", out)
	}
	if out.DefaultLevel != 3 || out.MaxWorkers != 2 || out.MaxEntrySize != 100 || out.MaxChunkSize != 10 {
		t.Fatalf("unexpected merged numeric options: %+v", out)
	}
	if out.DetectText || out.StoreIfAlreadyCompressed || out.SyncOnCommit || out.StrictVerify {
		t.Fatalf("No* flags must disable default-true booleans: %+v", out)
	}

	cases := []struct {
		in   error
		want error
	}{
		{format.ErrInvalidHeader, ErrInvalidFormat},
		{format.ErrInvalidDir, ErrInvalidFormat},
		{format.ErrInvalidChunk, ErrInvalidFormat},
		{format.ErrBadCodec, ErrUnsupportedCodec},
		{format.ErrLimitExceeded, ErrLimitExceeded},
		{format.ErrCRCMismatch, ErrCRCMismatch},
		{pathutil.ErrInvalidPath, ErrPathInvalid},
	}
	for _, tc := range cases {
		if !errors.Is(mapErr(tc.in), tc.want) {
			t.Fatalf("mapErr(%v): expected %v", tc.in, tc.want)
		}
	}
}

func TestAppendAtReadAllAndCloneEntry(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.bin")
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	if _, err := appendAt(f, 0, nil); err == nil {
		t.Fatalf("expected append zero data error")
	}
	next, err := appendAt(f, 0, []byte("abc"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if next != 3 {
		t.Fatalf("unexpected next offset: %d", next)
	}
	b, err := readAll(bytes.NewReader([]byte("xyz")))
	if err != nil || string(b) != "xyz" {
		t.Fatalf("readAll mismatch: %q err=%v", string(b), err)
	}

	e := format.EntryV1{Path: "a", FileSize: 1, Meta: []byte("m"), Chunks: []format.ChunkRefV1{{RawSize: 1}}}
	cl := cloneEntry(e)
	cl.Meta[0] = 'X'
	cl.Chunks[0].RawSize = 2
	if e.Meta[0] == 'X' || e.Chunks[0].RawSize == 2 {
		t.Fatalf("cloneEntry did not deep-copy slices")
	}
	info := entryInfo(e)
	if info.Path != "a" || info.Size != 1 {
		t.Fatalf("unexpected entryInfo: %+v", info)
	}
}

func TestReadEntriesByHeaderRejectsDuplicatePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.fbx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	blob, crc, err := format.EncodeDirectory(format.DirectoryV1{Entries: []format.EntryV1{{Path: "a"}, {Path: "a"}}})
	if err != nil {
		t.Fatalf("encode dir: %v", err)
	}
	h := format.HeaderV1{Magic: format.MagicHeader, Version: format.VersionV1, HeaderSize: format.HeaderSize, DirOffset: format.HeaderSize, DirSize: uint64(len(blob)), DirCRC32: crc}
	hb, _ := h.MarshalBinary()
	if _, err := f.WriteAt(hb, 0); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := f.WriteAt(blob, int64(h.DirOffset)); err != nil {
		t.Fatalf("write dir: %v", err)
	}
	if _, err := readEntriesByHeader(f, h); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("expected ErrInvalidFormat, got %v", err)
	}
}

func TestReadEntriesByHeaderRejectsOversizedDirectoryBlob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized-dir.fbx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	needSize := int64(format.HeaderSize) + int64(maxDirectoryBlobSize) + 1
	if err := f.Truncate(needSize); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	h := format.HeaderV1{
		Magic:      format.MagicHeader,
		Version:    format.VersionV1,
		HeaderSize: format.HeaderSize,
		DirOffset:  format.HeaderSize,
		DirSize:    maxDirectoryBlobSize + 1,
	}
	if _, err := readEntriesByHeader(f, h); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}

func TestReadEntriesByHeaderRejectsOutOfRangeChunkOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-chunk-offset.fbx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	blob, crc, err := format.EncodeDirectory(format.DirectoryV1{
		Entries: []format.EntryV1{
			{
				Path:     "a",
				FileSize: 1,
				Chunks:   []format.ChunkRefV1{{ChunkOffset: 1 << 20, RawOffset: 0, RawSize: 1, CompSize: 1, CRC32Raw: 1}},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode dir: %v", err)
	}
	h := format.HeaderV1{
		Magic:      format.MagicHeader,
		Version:    format.VersionV1,
		HeaderSize: format.HeaderSize,
		DirOffset:  format.HeaderSize,
		DirSize:    uint64(len(blob)),
		DirCRC32:   crc,
	}
	hb, _ := h.MarshalBinary()
	if _, err := f.WriteAt(hb, 0); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := f.WriteAt(blob, int64(h.DirOffset)); err != nil {
		t.Fatalf("write dir: %v", err)
	}
	if _, err := readEntriesByHeader(f, h); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("expected ErrInvalidFormat, got %v", err)
	}
}

func TestOpenReturnsLimitExceededFromPrimaryHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "open-limit.fbx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	h := format.HeaderV1{
		Magic:      format.MagicHeader,
		Version:    format.VersionV1,
		HeaderSize: format.HeaderSize,
		DirOffset:  format.HeaderSize,
		DirSize:    maxDirectoryBlobSize + 1,
	}
	hb, _ := h.MarshalBinary()
	if _, err := f.WriteAt(hb, 0); err != nil {
		_ = f.Close()
		t.Fatalf("write header: %v", err)
	}
	if err := f.Truncate(int64(h.DirOffset + h.DirSize)); err != nil {
		_ = f.Close()
		t.Fatalf("truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := Open(path, nil); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}

func TestEntryReaderHonorsChunkAndEntryLimits(t *testing.T) {
	rec, _, err := format.EncodeChunkRecord([]byte("hello"), format.CodecStore, 0)
	if err != nil {
		t.Fatalf("encode chunk: %v", err)
	}
	r := &entryReader{
		f:        bytes.NewReader(rec),
		chunks:   []format.ChunkRefV1{{ChunkOffset: 0, RawSize: 5, CompSize: uint32(len(rec) - 16), CRC32Raw: 0x3610a686}},
		maxChunk: 4,
	}
	buf := make([]byte, 8)
	if _, err := r.Read(buf); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected chunk ErrLimitExceeded, got %v", err)
	}

	r = &entryReader{
		f:         bytes.NewReader(rec),
		chunks:    []format.ChunkRefV1{{ChunkOffset: 0, RawSize: 5, CompSize: uint32(len(rec) - 16), CRC32Raw: 0x3610a686}},
		maxEntry:  4,
		verifyCRC: true,
	}
	if _, err := r.Read(buf); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected entry ErrLimitExceeded, got %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if r.cur != nil {
		t.Fatalf("expected current buffer reset")
	}
}

func TestEntryReaderRejectsChunkHeaderCompSizeMismatchBeforePayloadRead(t *testing.T) {
	rec := make([]byte, 16)
	rec[0] = 'C'
	rec[1] = 'K'
	rec[2] = byte(format.CodecStore)
	rec[3] = 0
	// Header claims huge compressed payload, but ref allows only 8.
	rec[4] = 5
	rec[8] = 0xFF
	rec[9] = 0xFF
	rec[10] = 0xFF
	rec[11] = 0x7F

	r := &entryReader{
		f:      bytes.NewReader(rec),
		chunks: []format.ChunkRefV1{{ChunkOffset: 0, RawSize: 5, CompSize: 8}},
	}
	buf := make([]byte, 8)
	if _, err := r.Read(buf); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}

func TestEntryReaderHonorsCompressedChunkLimit(t *testing.T) {
	r := &entryReader{
		f:        bytes.NewReader([]byte{}),
		chunks:   []format.ChunkRefV1{{ChunkOffset: 0, RawSize: 1, CompSize: 10}},
		maxChunk: 4,
	}
	buf := make([]byte, 1)
	if _, err := r.Read(buf); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded for comp-size limit, got %v", err)
	}
}

func TestReadAllPropagatesError(t *testing.T) {
	errBoom := errors.New("boom")
	r := io.MultiReader(bytes.NewBufferString("ok"), errReader{err: errBoom})
	_, err := readAll(r)
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected %v, got %v", errBoom, err)
	}
}

type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }

func TestCloseReturnsErrorWhileTransactionActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close-waits.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := c.Begin()
	if err != nil {
		_ = c.Close()
		t.Fatalf("begin: %v", err)
	}

	if err := c.Close(); err == nil {
		t.Fatalf("expected close to fail while tx active")
	}

	tx.Rollback()
	if err := c.Close(); err != nil {
		t.Fatalf("close failed after rollback: %v", err)
	}
}

func TestOpenReaderAfterCloseReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reader-after-close.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a", bytes.NewReader([]byte("x")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := c.OpenReader("a"); err == nil {
		t.Fatalf("expected error from OpenReader on closed container")
	}
}

func TestBeginBlocksConcurrentWritersAcrossContainersInProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "process-write-lock.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a", bytes.NewReader([]byte("x")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	c1, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open c1: %v", err)
	}
	defer c1.Close()
	c2, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open c2: %v", err)
	}
	defer c2.Close()

	tx1, err := c1.Begin()
	if err != nil {
		t.Fatalf("begin c1: %v", err)
	}
	defer tx1.Rollback()

	type beginResult struct {
		tx  *Tx
		err error
	}
	done := make(chan beginResult, 1)
	go func() {
		tx2, err := c2.Begin()
		done <- beginResult{tx: tx2, err: err}
	}()

	select {
	case r := <-done:
		if r.tx != nil {
			r.tx.Rollback()
		}
		t.Fatalf("second Begin must block while first tx holds writer lock (err=%v)", r.err)
	case <-time.After(120 * time.Millisecond):
	}

	tx1.Rollback()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("second Begin after release: %v", r.err)
		}
		if r.tx == nil {
			t.Fatalf("expected second Begin tx")
		}
		r.tx.Rollback()
	case <-time.After(2 * time.Second):
		t.Fatalf("second Begin did not proceed after first rollback")
	}
}

func TestOpenRecoversBeforeLazyAdoptionWhenDirectoryCRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lazy-open-recover.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.txt", bytes.NewReader([]byte("a")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add a: %v", err)
	}
	if err := c.Add("b.txt", bytes.NewReader([]byte("b")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add b: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	broken := readPrimaryHeader(t, path)
	if broken.Flags&format.HeaderFlagHasDirIndex == 0 {
		t.Fatalf("expected HAS_DIR_INDEX in test fixture")
	}
	// Corrupt one byte inside the latest directory payload to force CRC mismatch.
	corruptByteAt(t, path, int64(broken.DirOffset+20))

	recovered, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open with recovery: %v", err)
	}
	defer recovered.Close()

	recovered.mu.RLock()
	h := recovered.header
	recovered.mu.RUnlock()
	if h.DirOffset == broken.DirOffset && h.DirCRC32 == broken.DirCRC32 {
		t.Fatalf("expected recovered header to differ from corrupted snapshot")
	}

	if _, err := recovered.Stat("a.txt"); err != nil {
		t.Fatalf("stat recovered entry: %v", err)
	}
	it := recovered.List()
	count := 0
	for it.Next() {
		count++
	}
	if err := it.Err(); err != nil {
		t.Fatalf("list after recovery: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected at least one recovered entry")
	}
	if _, err := recovered.Verify(&VerifyOptions{Mode: VerifyDirectoryOnly}); err != nil {
		t.Fatalf("verify dir after recovery: %v", err)
	}
}
