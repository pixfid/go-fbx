package tests

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
)

func TestRoundTripAddExtractReplaceRemove(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "library.fbx")

	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer c.Close()

	orig := []byte("<FictionBook>hello</FictionBook>")
	if err := c.Add("books/a.fb2", bytes.NewReader(orig), []byte(`{"mime":"application/fb2+xml"}`), nil); err != nil {
		t.Fatalf("add: %v", err)
	}

	var out bytes.Buffer
	if err := c.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(out.Bytes(), orig) {
		t.Fatalf("content mismatch")
	}

	repl := []byte("<FictionBook>replaced</FictionBook>")
	if err := c.Replace("books/a.fb2", bytes.NewReader(repl), nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}
	out.Reset()
	if err := c.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract replaced: %v", err)
	}
	if !bytes.Equal(out.Bytes(), repl) {
		t.Fatalf("replaced content mismatch")
	}

	if err := c.Remove("books/a.fb2"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := c.Stat("books/a.fb2"); err == nil {
		t.Fatalf("expected not found after remove")
	}
}

func TestConvertZIPToFBX(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "in.zip")
	fbxPath := filepath.Join(dir, "out.fbx")

	if err := createZip(zipPath, map[string]string{
		"a/book1.fb2":   "book1",
		"img/cover.jpg": "jpegdata",
	}); err != nil {
		t.Fatalf("create zip: %v", err)
	}

	if err := fbx.ConvertZIPToFBX(zipPath, fbxPath, &fbx.ZIPImportOptions{IncludeMetadata: true}); err != nil {
		t.Fatalf("convert: %v", err)
	}

	c, err := fbx.Open(fbxPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()

	it := c.List()
	entries := map[string]fbx.EntryInfo{}
	for it.Next() {
		e := it.Value()
		entries[e.Path] = e
	}
	if err := it.Err(); err != nil {
		t.Fatalf("list err: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries got %d", len(entries))
	}

	var out bytes.Buffer
	if err := c.Extract("a/book1.fb2", &out); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if out.String() != "book1" {
		t.Fatalf("extract mismatch")
	}

	meta := entries["a/book1.fb2"].Meta
	if len(meta) == 0 {
		t.Fatalf("expected metadata")
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatalf("invalid metadata json: %v", err)
	}
	if _, ok := m["zip_name"]; !ok {
		t.Fatalf("metadata missing zip_name")
	}
}

func TestConvertZIPToFBXProgress(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "in.zip")
	fbxPath := filepath.Join(dir, "out.fbx")

	if err := createZip(zipPath, map[string]string{
		"a.fb2": "alpha",
		"b.fb2": "beta",
	}); err != nil {
		t.Fatalf("create zip: %v", err)
	}

	phases := map[string]int{}
	var filesTotal int
	var bytesTotal uint64
	err := fbx.ConvertZIPToFBX(zipPath, fbxPath, &fbx.ZIPImportOptions{
		IncludeMetadata: true,
		Progress: func(p fbx.ZIPImportProgress) {
			phases[p.Phase]++
			if p.Phase == "start" {
				filesTotal = p.FilesTotal
				bytesTotal = p.BytesTotal
			}
		},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if phases["start"] != 1 || phases["done"] != 1 {
		t.Fatalf("expected start and done events once, got %+v", phases)
	}
	if phases["file_start"] != 2 || phases["file_done"] != 2 {
		t.Fatalf("expected file events for both files, got %+v", phases)
	}
	if phases["file_progress"] == 0 {
		t.Fatalf("expected at least one file_progress event")
	}
	if filesTotal != 2 {
		t.Fatalf("expected filesTotal=2 got %d", filesTotal)
	}
	if bytesTotal == 0 {
		t.Fatalf("expected non-zero bytesTotal")
	}
}

func TestRoundTripWithCodecs(t *testing.T) {
	cases := []struct {
		name  string
		codec fbx.Codec
	}{
		{name: "zstd", codec: fbx.CodecZstd},
		{name: "lz4", codec: fbx.CodecLZ4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !supportsCodecName(tc.name) {
				t.Skipf("codec %s is not available in this build", tc.name)
			}
			dir := t.TempDir()
			containerPath := filepath.Join(dir, tc.name+".fbx")
			c, err := fbx.Create(containerPath, nil)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			defer c.Close()

			orig := bytes.Repeat([]byte("fb2-content-"), 2048)
			if err := c.Add("books/codec.fb2", bytes.NewReader(orig), nil, &fbx.WriteOptions{Codec: tc.codec}); err != nil {
				t.Fatalf("add with codec %s: %v", tc.name, err)
			}

			var out bytes.Buffer
			if err := c.Extract("books/codec.fb2", &out); err != nil {
				t.Fatalf("extract: %v", err)
			}
			if !bytes.Equal(orig, out.Bytes()) {
				t.Fatalf("codec roundtrip mismatch")
			}
		})
	}
}

func TestParallelWritePipeline(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "parallel.fbx")
	c, err := fbx.Create(containerPath, &fbx.Options{
		MaxWorkers:   4,
		ChunkSizeBin: 32 << 10,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer c.Close()

	orig := bytes.Repeat([]byte("parallel-pipeline-"), 40000)
	if err := c.Add("books/p.fb2", bytes.NewReader(orig), nil, &fbx.WriteOptions{
		Codec:     fbx.CodecStore,
		ChunkSize: 32 << 10,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	rep, err := c.Verify(&fbx.VerifyOptions{Mode: fbx.VerifyAllChunks})
	if err != nil {
		t.Fatalf("verify: %v (report=%+v)", err, rep)
	}
	var out bytes.Buffer
	if err := c.Extract("books/p.fb2", &out); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(orig, out.Bytes()) {
		t.Fatalf("parallel roundtrip mismatch")
	}
}

func TestStrictVerifyOption(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "strict.fbx")

	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	orig := []byte("strict-verify-content")
	if err := c.Add("books/a.fb2", bytes.NewReader(orig), nil, &fbx.WriteOptions{Codec: fbx.CodecStore}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	entry, err := readEntry(containerPath, "books/a.fb2")
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if len(entry.Chunks) == 0 {
		t.Fatalf("expected chunk")
	}
	if err := corruptFirstPayloadByte(containerPath, int64(entry.Chunks[0].ChunkOffset)); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	strict, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open strict: %v", err)
	}
	defer strict.Close()
	var out bytes.Buffer
	err = strict.Extract("books/a.fb2", &out)
	if !errors.Is(err, fbx.ErrCRCMismatch) {
		t.Fatalf("expected ErrCRCMismatch, got %v", err)
	}

	unsafe, err := fbx.Open(containerPath, &fbx.Options{StrictVerify: false})
	if err != nil {
		t.Fatalf("open unsafe: %v", err)
	}
	defer unsafe.Close()
	out.Reset()
	if err := unsafe.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("unsafe extract must succeed, got: %v", err)
	}
	if bytes.Equal(out.Bytes(), orig) {
		t.Fatalf("expected corrupted output under StrictVerify=false")
	}
}

func TestWriteLimitMaxEntrySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "limit-write.fbx")
	c, err := fbx.Create(path, &fbx.Options{MaxEntrySize: 8})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer c.Close()

	err = c.Add("a.fb2", bytes.NewReader([]byte("123456789")), nil, nil)
	if !errors.Is(err, fbx.ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}

func TestReadLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "limit-read.fbx")
	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := bytes.Repeat([]byte("x"), 64)
	if err := c.Add("a.fb2", bytes.NewReader(body), nil, &fbx.WriteOptions{ChunkSize: 64}); err != nil {
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	c1, err := fbx.Open(path, &fbx.Options{MaxEntrySize: 32})
	if err != nil {
		t.Fatalf("open max entry: %v", err)
	}
	defer c1.Close()
	var out bytes.Buffer
	err = c1.Extract("a.fb2", &out)
	if !errors.Is(err, fbx.ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded on max entry, got %v", err)
	}

	c2, err := fbx.Open(path, &fbx.Options{MaxChunkSize: 16})
	if err != nil {
		t.Fatalf("open max chunk: %v", err)
	}
	defer c2.Close()
	out.Reset()
	err = c2.Extract("a.fb2", &out)
	if !errors.Is(err, fbx.ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded on max chunk, got %v", err)
	}
}

func TestDetectTextAndChunkSizeSelection(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "detect.fbx")
	c, err := fbx.Create(containerPath, &fbx.Options{
		DetectText:    true,
		ChunkSizeText: 16,
		ChunkSizeBin:  128,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer c.Close()

	textBody := bytes.Repeat([]byte("a"), 100)
	binBody := bytes.Repeat([]byte{0xff, 0xd8, 0x00, 0x11}, 25)
	if err := c.Add("books/a.fb2", bytes.NewReader(textBody), nil, nil); err != nil {
		t.Fatalf("add text: %v", err)
	}
	if err := c.Add("img/a.jpg", bytes.NewReader(binBody), nil, nil); err != nil {
		t.Fatalf("add bin: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	textEntry, err := readEntry(containerPath, "books/a.fb2")
	if err != nil {
		t.Fatalf("read text entry: %v", err)
	}
	binEntry, err := readEntry(containerPath, "img/a.jpg")
	if err != nil {
		t.Fatalf("read bin entry: %v", err)
	}
	if textEntry.EntryFlags&uint32(fbx.EntryFlagIsText) == 0 {
		t.Fatalf("expected text flag for books/a.fb2")
	}
	if binEntry.EntryFlags&uint32(fbx.EntryFlagIsBinary) == 0 {
		t.Fatalf("expected binary flag for img/a.jpg")
	}
	if len(textEntry.Chunks) <= len(binEntry.Chunks) {
		t.Fatalf("expected text to have more chunks with smaller ChunkSizeText; text=%d bin=%d", len(textEntry.Chunks), len(binEntry.Chunks))
	}
}

func TestStoreIfAlreadyCompressedOption(t *testing.T) {
	if !supportsCodecName("zstd") {
		t.Skip("codec zstd is not available in this build")
	}
	dir := t.TempDir()
	body := bytes.Repeat([]byte{0xff, 0xd8, 0x00, 0x11}, 1024)

	pathStore := filepath.Join(dir, "store.fbx")
	c1, err := fbx.Create(pathStore, &fbx.Options{
		DefaultCodec:             fbx.CodecZstd,
		StoreIfAlreadyCompressed: true,
	})
	if err != nil {
		t.Fatalf("create store=true: %v", err)
	}
	if err := c1.Add("img/pic.jpg", bytes.NewReader(body), nil, nil); err != nil {
		t.Fatalf("add store=true: %v", err)
	}
	_ = c1.Close()
	codec1, err := firstChunkCodec(pathStore, "img/pic.jpg")
	if err != nil {
		t.Fatalf("codec store=true: %v", err)
	}
	if codec1 != format.CodecStore {
		t.Fatalf("expected STORE for compressed extension, got %d", codec1)
	}

	pathZstd := filepath.Join(dir, "zstd.fbx")
	c2, err := fbx.Create(pathZstd, &fbx.Options{
		DefaultCodec:             fbx.CodecZstd,
		StoreIfAlreadyCompressed: false,
	})
	if err != nil {
		t.Fatalf("create store=false: %v", err)
	}
	if err := c2.Add("img/pic.jpg", bytes.NewReader(body), nil, nil); err != nil {
		t.Fatalf("add store=false: %v", err)
	}
	_ = c2.Close()
	codec2, err := firstChunkCodec(pathZstd, "img/pic.jpg")
	if err != nil {
		t.Fatalf("codec store=false: %v", err)
	}
	if codec2 != format.CodecZstd {
		t.Fatalf("expected ZSTD with StoreIfAlreadyCompressed=false, got %d", codec2)
	}
}

func TestOpenRecoversFromJournalWhenHeaderCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recover-corrupt-header.fbx")
	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := []byte("journal-recovery-1")
	if err := c.Add("books/a.fb2", bytes.NewReader(body), nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open for corrupt: %v", err)
	}
	zero := make([]byte, format.HeaderSize)
	if _, err := f.WriteAt(zero, 0); err != nil {
		_ = f.Close()
		t.Fatalf("corrupt header: %v", err)
	}
	_ = f.Close()

	recovered, err := fbx.Open(path, nil)
	if err != nil {
		t.Fatalf("open with recovery: %v", err)
	}
	defer recovered.Close()
	var out bytes.Buffer
	if err := recovered.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract after recovery: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("recovered content mismatch")
	}
}

func TestOpenRecoversFromJournalWhenDirPointerInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recover-bad-dir.fbx")
	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := []byte("journal-recovery-2")
	if err := c.Add("books/a.fb2", bytes.NewReader(body), nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open for mutate: %v", err)
	}
	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		_ = f.Close()
		t.Fatalf("read header: %v", err)
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		_ = f.Close()
		t.Fatalf("unmarshal header: %v", err)
	}
	h.DirOffset = ^uint64(0) - 128
	newHead, err := h.MarshalBinary()
	if err != nil {
		_ = f.Close()
		t.Fatalf("marshal mutated header: %v", err)
	}
	if _, err := f.WriteAt(newHead, 0); err != nil {
		_ = f.Close()
		t.Fatalf("write mutated header: %v", err)
	}
	_ = f.Close()

	recovered, err := fbx.Open(path, nil)
	if err != nil {
		t.Fatalf("open with recovery: %v", err)
	}
	defer recovered.Close()
	var out bytes.Buffer
	if err := recovered.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract after recovery: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("recovered content mismatch")
	}
}

func TestOpenRecoversFromBackupWhenJournalBroken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recover-backup.fbx")
	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := []byte("backup-recovery")
	if err := c.Add("books/a.fb2", bytes.NewReader(body), nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := corruptPrimaryHeader(path); err != nil {
		t.Fatalf("corrupt header: %v", err)
	}
	if err := corruptLatestRecordMagic(path, "JNL1"); err != nil {
		t.Fatalf("corrupt journal magic: %v", err)
	}

	recovered, err := fbx.Open(path, nil)
	if err != nil {
		t.Fatalf("open with backup recovery: %v", err)
	}
	defer recovered.Close()
	var out bytes.Buffer
	if err := recovered.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract after backup recovery: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("backup recovered content mismatch")
	}
}

func TestOpenRecoversFromFixedBackupWhenRecordsBroken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recover-fixed-backup.fbx")
	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := []byte("fixed-backup-recovery")
	if err := c.Add("books/a.fb2", bytes.NewReader(body), nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := corruptPrimaryHeader(path); err != nil {
		t.Fatalf("corrupt header: %v", err)
	}
	if err := corruptLatestRecordMagic(path, "JNL1"); err != nil {
		t.Fatalf("corrupt journal magic: %v", err)
	}
	if err := corruptLatestRecordMagic(path, "BKP1"); err != nil {
		t.Fatalf("corrupt backup-record magic: %v", err)
	}

	recovered, err := fbx.Open(path, nil)
	if err != nil {
		t.Fatalf("open with fixed backup recovery: %v", err)
	}
	defer recovered.Close()
	var out bytes.Buffer
	if err := recovered.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract after fixed backup recovery: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("fixed backup recovered content mismatch")
	}
}

func TestPackOutOfPlace(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.fbx")
	dstPath := filepath.Join(dir, "packed.fbx")

	c, err := fbx.Create(srcPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	large := bytes.Repeat([]byte("L"), 512*1024)
	if err := c.Add("books/a.fb2", bytes.NewReader(large), nil, nil); err != nil {
		t.Fatalf("add large: %v", err)
	}
	if err := c.Replace("books/a.fb2", bytes.NewReader([]byte("small-a")), nil, nil); err != nil {
		t.Fatalf("replace a: %v", err)
	}
	if err := c.Add("trash/tmp.bin", bytes.NewReader(large), nil, nil); err != nil {
		t.Fatalf("add tmp: %v", err)
	}
	if err := c.Remove("trash/tmp.bin"); err != nil {
		t.Fatalf("remove tmp: %v", err)
	}
	if err := c.Add("books/b.fb2", bytes.NewReader([]byte("small-b")), nil, nil); err != nil {
		t.Fatalf("add b: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close src: %v", err)
	}

	before, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	if err := fbx.Pack(srcPath, dstPath, &fbx.PackOptions{
		Codec:    fbx.CodecStore,
		VerifyIn: true,
	}); err != nil {
		t.Fatalf("pack: %v", err)
	}
	after, err := os.Stat(dstPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("expected packed file to be smaller: before=%d after=%d", before.Size(), after.Size())
	}

	packed, err := fbx.Open(dstPath, nil)
	if err != nil {
		t.Fatalf("open packed: %v", err)
	}
	defer packed.Close()

	var out bytes.Buffer
	if err := packed.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract a: %v", err)
	}
	if out.String() != "small-a" {
		t.Fatalf("unexpected a content: %q", out.String())
	}
	out.Reset()
	if err := packed.Extract("books/b.fb2", &out); err != nil {
		t.Fatalf("extract b: %v", err)
	}
	if out.String() != "small-b" {
		t.Fatalf("unexpected b content: %q", out.String())
	}
	if _, err := packed.Stat("trash/tmp.bin"); err == nil {
		t.Fatalf("removed entry must not exist after pack")
	}
}

func TestPackInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inplace.fbx")

	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	large := bytes.Repeat([]byte("X"), 256*1024)
	if err := c.Add("books/a.fb2", bytes.NewReader(large), nil, nil); err != nil {
		t.Fatalf("add large: %v", err)
	}
	if err := c.Replace("books/a.fb2", bytes.NewReader([]byte("in-place")), nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := fbx.Pack(path, path, &fbx.PackOptions{Codec: fbx.CodecStore, VerifyIn: true}); err != nil {
		t.Fatalf("pack in-place: %v", err)
	}
	reopened, err := fbx.Open(path, nil)
	if err != nil {
		t.Fatalf("open after pack: %v", err)
	}
	defer reopened.Close()
	var out bytes.Buffer
	if err := reopened.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract after pack: %v", err)
	}
	if out.String() != "in-place" {
		t.Fatalf("unexpected content after in-place pack: %q", out.String())
	}
}

func TestTxRemovePrefixAndGlob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.fbx")
	c, err := fbx.Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer c.Close()

	seed := map[string]string{
		"books/a.fb2":      "a",
		"books/b.fb2":      "b",
		"books/sub/c.fb2":  "c",
		"images/cover.jpg": "img",
	}
	tx, err := c.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for p, body := range seed {
		if err := tx.Upsert(p, bytes.NewReader([]byte(body)), nil, nil); err != nil {
			t.Fatalf("seed upsert %s: %v", p, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	tx, err = c.Begin()
	if err != nil {
		t.Fatalf("begin remove prefix: %v", err)
	}
	removed, err := tx.RemovePrefix("books/sub")
	if err != nil {
		t.Fatalf("RemovePrefix: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected RemovePrefix removed=1 got %d", removed)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit remove prefix: %v", err)
	}

	if _, err := c.Stat("books/sub/c.fb2"); err == nil {
		t.Fatalf("books/sub/c.fb2 should be removed by prefix")
	}

	tx, err = c.Begin()
	if err != nil {
		t.Fatalf("begin remove glob: %v", err)
	}
	removed, err = tx.RemoveGlob("books/*.fb2")
	if err != nil {
		t.Fatalf("RemoveGlob: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected RemoveGlob removed=2 got %d", removed)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit remove glob: %v", err)
	}

	if _, err := c.Stat("books/a.fb2"); err == nil {
		t.Fatalf("books/a.fb2 should be removed by glob")
	}
	if _, err := c.Stat("books/b.fb2"); err == nil {
		t.Fatalf("books/b.fb2 should be removed by glob")
	}
	if _, err := c.Stat("images/cover.jpg"); err != nil {
		t.Fatalf("images/cover.jpg should remain, got: %v", err)
	}

	tx, err = c.Begin()
	if err != nil {
		t.Fatalf("begin remove where: %v", err)
	}
	removed, err = tx.RemoveWhere(func(info fbx.EntryInfo) bool {
		return info.Path == "images/cover.jpg"
	})
	if err != nil {
		t.Fatalf("RemoveWhere: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected RemoveWhere removed=1 got %d", removed)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit remove where: %v", err)
	}
	if _, err := c.Stat("images/cover.jpg"); err == nil {
		t.Fatalf("images/cover.jpg should be removed by RemoveWhere")
	}
}

func createZip(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, content); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return nil
}

func readEntry(containerPath, entryPath string) (format.EntryV1, error) {
	entries, _, err := readDirectory(containerPath)
	if err != nil {
		return format.EntryV1{}, err
	}
	e, ok := entries[entryPath]
	if !ok {
		return format.EntryV1{}, fbx.ErrNotFound
	}
	return e, nil
}

func firstChunkCodec(containerPath, entryPath string) (format.Codec, error) {
	e, err := readEntry(containerPath, entryPath)
	if err != nil {
		return 0, err
	}
	if len(e.Chunks) == 0 {
		return 0, errors.New("no chunks")
	}
	f, err := os.Open(containerPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	rec, err := format.ReadChunkRecordAtOpt(f, int64(e.Chunks[0].ChunkOffset), false)
	if err != nil {
		return 0, err
	}
	return rec.Codec, nil
}

func readDirectory(containerPath string) (map[string]format.EntryV1, format.HeaderV1, error) {
	f, err := os.Open(containerPath)
	if err != nil {
		return nil, format.HeaderV1{}, err
	}
	defer f.Close()
	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		return nil, format.HeaderV1{}, err
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		return nil, format.HeaderV1{}, err
	}
	blob := make([]byte, h.DirSize)
	if _, err := f.ReadAt(blob, int64(h.DirOffset)); err != nil {
		return nil, format.HeaderV1{}, err
	}
	d, err := format.DecodeDirectory(blob, h.DirCRC32, h.DirSize)
	if err != nil {
		return nil, format.HeaderV1{}, err
	}
	out := make(map[string]format.EntryV1, len(d.Entries))
	for _, e := range d.Entries {
		out[e.Path] = e
	}
	return out, h, nil
}

func corruptFirstPayloadByte(containerPath string, chunkOffset int64) error {
	f, err := os.OpenFile(containerPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, chunkOffset+16); err != nil {
		return err
	}
	b[0] ^= 0x01
	if _, err := f.WriteAt(b, chunkOffset+16); err != nil {
		return err
	}
	return f.Sync()
}

func corruptPrimaryHeader(containerPath string) error {
	f, err := os.OpenFile(containerPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	zero := make([]byte, format.HeaderSize)
	if _, err := f.WriteAt(zero, 0); err != nil {
		return err
	}
	return f.Sync()
}

func corruptLatestRecordMagic(containerPath, magic string) error {
	f, err := os.OpenFile(containerPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	const scanWindow = 8 << 20
	size := st.Size()
	window := int64(scanWindow)
	if size < window {
		window = size
	}
	start := size - window
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, start); err != nil {
		return err
	}
	needle := []byte(magic)
	for i := len(buf) - 4; i >= 0; i-- {
		if bytes.Equal(buf[i:i+4], needle) {
			abs := start + int64(i)
			b := []byte{needle[0] ^ 0x01}
			if _, err := f.WriteAt(b, abs); err != nil {
				return err
			}
			return f.Sync()
		}
	}
	return errors.New("record magic not found")
}
