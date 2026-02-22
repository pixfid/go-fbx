package format

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func TestDirectoryEncodeDecodeRoundTrip(t *testing.T) {
	dir := DirectoryV1{BuildUnix: 42, Entries: []EntryV1{
		{Path: "b.fb2", FileSize: 3, Chunks: []ChunkRefV1{{ChunkOffset: 10, RawOffset: 0, RawSize: 3, CompSize: 3, CRC32Raw: 1}}, Meta: []byte("m2")},
		{Path: "a.fb2", FileSize: 2, Chunks: []ChunkRefV1{{ChunkOffset: 20, RawOffset: 0, RawSize: 2, CompSize: 2, CRC32Raw: 2}}, Meta: []byte("m1")},
	}}
	blob, crc, err := EncodeDirectory(dir)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeDirectory(blob, crc, uint64(len(blob)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 2 || out.Entries[0].Path != "a.fb2" || out.Entries[1].Path != "b.fb2" {
		t.Fatalf("entries should be sorted by path: %+v", out.Entries)
	}
}

func TestDirectoryDecodeValidations(t *testing.T) {
	dir := DirectoryV1{Entries: []EntryV1{{Path: "a", FileSize: 1, Chunks: []ChunkRefV1{{ChunkOffset: 1, RawOffset: 0, RawSize: 1, CompSize: 1, CRC32Raw: 1}}}}}
	blob, crc, err := EncodeDirectory(dir)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeDirectory(blob, crc+1, uint64(len(blob))); err == nil {
		t.Fatalf("expected CRC mismatch error")
	}
	if _, err := DecodeDirectory(blob, crc, uint64(len(blob)+1)); err == nil {
		t.Fatalf("expected size mismatch error")
	}

	// Corrupt chunk reserved field (must be zero).
	corrupt := append([]byte(nil), blob...)
	entryOffset := 20
	chunkReservedOffset := entryOffset + 44 + 28
	corrupt[chunkReservedOffset] = 1
	newCRC := crc32.ChecksumIEEE(corrupt[:len(corrupt)-dirFooterSize])
	binary.LittleEndian.PutUint32(corrupt[len(corrupt)-12:len(corrupt)-8], newCRC)
	if _, err := DecodeDirectory(corrupt, newCRC, uint64(len(corrupt))); err == nil {
		t.Fatalf("expected reserved-field validation error")
	}

	if _, _, err := EncodeDirectory(DirectoryV1{Flags: 1}); !errors.Is(err, ErrInvalidDir) {
		t.Fatalf("expected ErrInvalidDir on non-zero flags, got %v", err)
	}
}
