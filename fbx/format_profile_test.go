package fbx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/internal/format"
)

func TestCreateWritesCanonicalV1Layout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "canonical-empty.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	h, err := readPrimaryHeaderFile(f)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if err := validateCanonicalHeaderLayout(h); err != nil {
		t.Fatalf("expected canonical header layout, got %v", err)
	}
	if readHeaderGeneration(h) == 0 {
		t.Fatalf("expected non-zero initial generation")
	}

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open canonical container: %v", err)
	}
	defer co.Close()
	if co.lazyDirIndex == nil || len(co.lazyDirBlob) == 0 {
		t.Fatalf("expected lazy directory state for canonical open")
	}
}

func TestReadEntriesByHeaderRejectsLegacySnapshotLayout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-header.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader([]byte("x")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	h, err := readPrimaryHeaderFile(f)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	h.Flags &^= format.HeaderFlagHasDirIndex | format.HeaderFlagHasRequiredFeatures
	h.Reserved[headerReservedLayoutMarkerOffset] = 0
	h.Reserved[headerReservedLayoutMinorOffset] = 0
	writeHeaderGeneration(&h, 0)
	writeHeaderDirIndexPointer(&h, dirIndexPointer{})
	writeHeaderRequiredFeaturesLow(&h, 0)
	buf, err := h.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal legacy header: %v", err)
	}
	if _, err := f.WriteAt(buf, 0); err != nil {
		t.Fatalf("rewrite header: %v", err)
	}
	if _, err := readEntriesByHeader(f, h); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("expected ErrInvalidFormat for non-canonical layout, got %v", err)
	}
}

func TestReadEntriesByHeaderDetectsCorruptDirIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idx-corrupt.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader([]byte("x")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	h, err := readPrimaryHeaderFile(f)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	ptr := readHeaderDirIndexPointer(h)
	if ptr.offset == 0 || ptr.size == 0 {
		t.Fatalf("missing idx pointer")
	}
	if _, err := f.WriteAt([]byte{0x00}, int64(ptr.offset)); err != nil {
		t.Fatalf("corrupt idx: %v", err)
	}
	if _, err := readEntriesByHeader(f, h); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("expected ErrCRCMismatch, got %v", err)
	}
}

func TestOpenUsesLazyDirIndexSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lazy-open.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader([]byte("a")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add a: %v", err)
	}
	if err := c.Add("books/b.fb2", bytes.NewReader([]byte("b")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add b: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer co.Close()

	if co.entries != nil {
		t.Fatalf("expected lazy open without eager entries map")
	}
	if co.lazyDirIndex == nil || len(co.lazyDirBlob) == 0 {
		t.Fatalf("expected lazy dir index state to be populated")
	}
	info, err := co.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("lazy stat: %v", err)
	}
	if info.Path != "books/a.fb2" {
		t.Fatalf("unexpected stat path: %s", info.Path)
	}

	tx, err := co.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	tx.Rollback()
	if co.entries == nil {
		t.Fatalf("expected begin() to materialize entries map")
	}
	if co.lazyDirIndex != nil || len(co.lazyDirBlob) != 0 {
		t.Fatalf("expected lazy state to be dropped after materialization")
	}
}

func readPrimaryHeaderFile(f *os.File) (format.HeaderV1, error) {
	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		return format.HeaderV1{}, err
	}
	return format.UnmarshalHeaderV1(headBuf)
}

func readPrimaryHeaderAndEntries(path string) (format.HeaderV1, map[string]format.EntryV1, error) {
	f, err := os.Open(path)
	if err != nil {
		return format.HeaderV1{}, nil, err
	}
	defer f.Close()
	h, err := readPrimaryHeaderFile(f)
	if err != nil {
		return format.HeaderV1{}, nil, err
	}
	entries, err := readEntriesByHeader(f, h)
	if err != nil {
		return format.HeaderV1{}, nil, err
	}
	return h, entries, nil
}

func readPrimaryEntries(path string) (map[string]format.EntryV1, error) {
	_, entries, err := readPrimaryHeaderAndEntries(path)
	return entries, err
}
