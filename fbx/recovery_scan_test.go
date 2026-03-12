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

func TestOpenRecoversByDirectoryScanWhenHeadersAndRecordsAreLost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan_recover.fbx")
	body := bytes.Repeat([]byte("scan-recovery-body-"), 32)

	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader(body), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	corruptPrimaryFixedAndAllTailRecords(t, path)

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open with directory scan recovery: %v", err)
	}
	defer co.Close()
	if co.header.Flags != 0 {
		t.Fatalf("expected synthesized header flags to be zero, got %#x", co.header.Flags)
	}

	var out bytes.Buffer
	if err := co.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract recovered entry: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("recovered payload mismatch")
	}
}

func TestOpenDirectoryScanChoosesLatestValidDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan_latest.fbx")
	oldBody := bytes.Repeat([]byte("version-one-"), 24)
	newBody := bytes.Repeat([]byte("version-two-"), 24)

	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Upsert("books/a.fb2", bytes.NewReader(oldBody), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert old: %v", err)
	}
	if err := c.Upsert("books/a.fb2", bytes.NewReader(newBody), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert new: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	corruptPrimaryFixedAndAllTailRecords(t, path)

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open with scan recovery: %v", err)
	}
	defer co.Close()

	var out bytes.Buffer
	if err := co.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract latest entry: %v", err)
	}
	if !bytes.Equal(out.Bytes(), newBody) {
		t.Fatalf("expected latest directory snapshot to win")
	}
}

func TestOpenDirectoryScanFallsBackToPreviousDirectoryWhenLatestIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan_prev.fbx")
	oldBody := bytes.Repeat([]byte("old-body-"), 40)
	newBody := bytes.Repeat([]byte("new-body-"), 40)

	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Upsert("books/a.fb2", bytes.NewReader(oldBody), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert old: %v", err)
	}
	if err := c.Upsert("books/a.fb2", bytes.NewReader(newBody), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert new: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	h := readPrimaryHeader(t, path)
	if h.DirSize < 32 {
		t.Fatalf("unexpected tiny directory size: %d", h.DirSize)
	}
	corruptByteAt(t, path, int64(h.DirOffset+24))
	corruptPrimaryFixedAndAllTailRecords(t, path)

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open with scan recovery: %v", err)
	}
	defer co.Close()

	var out bytes.Buffer
	if err := co.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract fallback entry: %v", err)
	}
	if !bytes.Equal(out.Bytes(), oldBody) {
		t.Fatalf("expected fallback to previous valid directory snapshot")
	}
}

func TestOpenDirectoryScanPrefersMostReadableSnapshotOverNewest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan_quality.fbx")
	oldBody := bytes.Repeat([]byte("old-good-"), 64)
	newBody := bytes.Repeat([]byte("new-bad-"), 64)

	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Upsert("books/a.fb2", bytes.NewReader(oldBody), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert old: %v", err)
	}
	if err := c.Upsert("books/a.fb2", bytes.NewReader(newBody), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert new: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	corruptCurrentHeadChunkByte(t, path, "books/a.fb2")
	corruptPrimaryFixedAndAllTailRecords(t, path)

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open with scan recovery: %v", err)
	}
	defer co.Close()

	var out bytes.Buffer
	if err := co.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract recovered entry: %v", err)
	}
	if !bytes.Equal(out.Bytes(), oldBody) {
		t.Fatalf("expected recovery to choose most readable snapshot")
	}
}

func TestReadDirectoryCandidateAtRejectsOverflowedEntrySizes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan_overflow.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 20+44+16)
	copy(buf[:4], format.MagicDir[:])
	binary.LittleEndian.PutUint32(buf[4:8], 1)  // entryCount
	binary.LittleEndian.PutUint32(buf[8:12], 0) // flags
	// Entry header starts at offset 20; chunkCount at +32.
	binary.LittleEndian.PutUint32(buf[20+32:20+36], ^uint32(0))
	copy(buf[len(buf)-16:len(buf)-12], format.MagicEnd[:])
	binary.LittleEndian.PutUint64(buf[len(buf)-8:], uint64(len(buf)))

	if _, err := f.WriteAt(buf, 0); err != nil {
		t.Fatalf("write: %v", err)
	}
	dirOut, _, _, err := readDirectoryCandidateAt(f, 0, uint64(len(buf)))
	if !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("expected ErrInvalidFormat, got dir=%+v err=%v", dirOut, err)
	}
}

func readPrimaryHeader(t *testing.T, path string) format.HeaderV1 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	buf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(buf, 0); err != nil {
		t.Fatalf("read header: %v", err)
	}
	h, err := format.UnmarshalHeaderV1(buf)
	if err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	return h
}

func corruptPrimaryFixedAndAllTailRecords(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	raw := make([]byte, st.Size())
	if _, err := f.ReadAt(raw, 0); err != nil {
		t.Fatalf("read all: %v", err)
	}

	flipFileByte(t, f, 0)
	flipFileByte(t, f, fixedBackupOffset)
	corruptRecordMagicHits(t, f, raw, journalMagic[:])
	corruptRecordMagicHits(t, f, raw, backupMagic[:])
}

func corruptRecordMagicHits(t *testing.T, f *os.File, raw []byte, magic []byte) {
	t.Helper()
	for start := 0; start < len(raw); {
		idx := bytes.Index(raw[start:], magic)
		if idx < 0 {
			return
		}
		flipFileByte(t, f, int64(start+idx))
		start += idx + 1
	}
}

func corruptByteAt(t *testing.T, path string, off int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	defer f.Close()
	flipFileByte(t, f, off)
}

func flipFileByte(t *testing.T, f *os.File, off int64) {
	t.Helper()
	b := []byte{0}
	if _, err := f.ReadAt(b, off); err != nil {
		t.Fatalf("read byte at %d: %v", off, err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, off); err != nil {
		t.Fatalf("write byte at %d: %v", off, err)
	}
}

func corruptCurrentHeadChunkByte(t *testing.T, path, entryPath string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	defer f.Close()
	h := readPrimaryHeader(t, path)
	entries, err := readEntriesByHeader(f, h)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	e, ok := entries[entryPath]
	if !ok {
		t.Fatalf("entry not found in head snapshot: %s", entryPath)
	}
	if len(e.Chunks) == 0 {
		t.Fatalf("entry has no chunks: %s", entryPath)
	}
	flipFileByte(t, f, int64(e.Chunks[0].ChunkOffset+16))
}
