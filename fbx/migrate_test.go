package fbx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pixfid/go-fbx/internal/format"
)

func TestMigratePreservesChunkRefsAndPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migrate.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bodyA := bytes.Repeat([]byte("alpha-"), 256)
	bodyB := bytes.Repeat([]byte("beta-"), 128)
	if err := c.Add("books/a.fb2", bytes.NewReader(bodyA), []byte(`{"id":1}`), &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("add a: %v", err)
	}
	if err := c.Add("books/b.fb2", bytes.NewReader(bodyB), []byte(`{"id":2}`), &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("add b: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := rewritePrimaryHeaderAsLegacy(path); err != nil {
		t.Fatalf("rewrite primary header to legacy: %v", err)
	}

	pre, err := readPrimaryEntries(path)
	if err != nil {
		t.Fatalf("read pre entries: %v", err)
	}

	if err := Migrate(path, &MigrateOptions{VerifySource: VerifyDirectoryOnly, VerifyTarget: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h, post, err := readPrimaryHeaderAndEntries(path)
	if err != nil {
		t.Fatalf("read post entries: %v", err)
	}
	if !headerHasDirIndexExtension(h) {
		t.Fatalf("expected extension header after migrate, flags=%#x reserved[0]=%d", h.Flags, h.Reserved[0])
	}
	if h.Flags&format.HeaderFlagHasJournal == 0 || h.Flags&format.HeaderFlagHasBackup == 0 || h.Flags&format.HeaderFlagHasDirIndex == 0 || h.Flags&format.HeaderFlagHasRequiredFeatures == 0 {
		t.Fatalf("missing expected header flags: %#x", h.Flags)
	}
	if readHeaderGeneration(h) == 0 {
		t.Fatalf("expected generation > 0 after migrate")
	}
	if !reflect.DeepEqual(post, pre) {
		t.Fatalf("entries changed after migrate")
	}

	co, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open migrated: %v", err)
	}
	defer co.Close()
	var out bytes.Buffer
	if err := co.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract a: %v", err)
	}
	if !bytes.Equal(out.Bytes(), bodyA) {
		t.Fatalf("payload a mismatch")
	}
	out.Reset()
	if err := co.Extract("books/b.fb2", &out); err != nil {
		t.Fatalf("extract b: %v", err)
	}
	if !bytes.Equal(out.Bytes(), bodyB) {
		t.Fatalf("payload b mismatch")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migrate-idempotent.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader([]byte("x")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	if err := Migrate(path, nil); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after first migrate: %v", err)
	}
	if err := Migrate(path, nil); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after second migrate: %v", err)
	}
	if st2.Size() != st1.Size() {
		t.Fatalf("expected idempotent migrate not to append, size1=%d size2=%d", st1.Size(), st2.Size())
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
	_ = c.Close()
	if err := Migrate(path, nil); err != nil {
		t.Fatalf("migrate: %v", err)
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
	if err := Migrate(path, nil); err != nil {
		t.Fatalf("migrate: %v", err)
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

func rewritePrimaryHeaderAsLegacy(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	h, err := readPrimaryHeaderFile(f)
	if err != nil {
		return err
	}
	h.Flags &^= format.HeaderFlagHasDirIndex | format.HeaderFlagHasRequiredFeatures
	h.Reserved[headerReservedLayoutMarkerOffset] = headerLayoutMarkerLegacyBackupSlot
	h.Reserved[headerReservedLayoutMinorOffset] = 0
	writeHeaderGeneration(&h, 0)
	writeHeaderDirIndexPointer(&h, dirIndexPointer{})
	writeHeaderRequiredFeaturesLow(&h, 0)
	buf, err := h.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = f.WriteAt(buf, 0)
	return err
}
