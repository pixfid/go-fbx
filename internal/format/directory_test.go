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

func TestDecodeDirectoryRejectsOversizedEntryFieldsWithoutPanic(t *testing.T) {
	base := DirectoryV1{
		Entries: []EntryV1{
			{
				Path:     "a",
				FileSize: 1,
				Chunks:   []ChunkRefV1{{ChunkOffset: 1, RawOffset: 0, RawSize: 1, CompSize: 1, CRC32Raw: 1}},
			},
		},
	}
	blob, _, err := EncodeDirectory(base)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	entryOffset := dirHeaderSize

	mutate := func(offset int, value uint32) []byte {
		out := append([]byte(nil), blob...)
		binary.LittleEndian.PutUint32(out[offset:offset+4], value)
		newCRC := crc32.ChecksumIEEE(out[:len(out)-dirFooterSize])
		binary.LittleEndian.PutUint32(out[len(out)-12:len(out)-8], newCRC)
		return out
	}

	cases := []struct {
		name   string
		offset int
		value  uint32
	}{
		{name: "chunk-count", offset: entryOffset + 32, value: ^uint32(0)},
		{name: "meta-size", offset: entryOffset + 36, value: ^uint32(0)},
		{name: "path-size", offset: entryOffset + 40, value: ^uint32(0)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("DecodeDirectory panicked: %v", r)
				}
			}()
			corrupt := mutate(tc.offset, tc.value)
			crc := binary.LittleEndian.Uint32(corrupt[len(corrupt)-12 : len(corrupt)-8])
			if _, err := DecodeDirectory(corrupt, crc, uint64(len(corrupt))); !errors.Is(err, ErrInvalidDir) {
				t.Fatalf("expected ErrInvalidDir, got %v", err)
			}
		})
	}
}

func TestDecodeDirectoryRejectsChunkRawRangeOverflow(t *testing.T) {
	base := DirectoryV1{
		Entries: []EntryV1{
			{
				Path:     "a",
				FileSize: 1,
				Chunks:   []ChunkRefV1{{ChunkOffset: 1, RawOffset: 0, RawSize: 1, CompSize: 1, CRC32Raw: 1}},
			},
		},
	}
	blob, _, err := EncodeDirectory(base)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	corrupt := append([]byte(nil), blob...)
	entryOffset := dirHeaderSize
	fileSizePos := entryOffset + 24
	chunkPos := entryOffset + dirEntryHeadSize
	rawOffsetPos := chunkPos + 8
	rawSizePos := chunkPos + 16
	binary.LittleEndian.PutUint64(corrupt[fileSizePos:fileSizePos+8], 6)
	binary.LittleEndian.PutUint64(corrupt[rawOffsetPos:rawOffsetPos+8], ^uint64(0)-3)
	binary.LittleEndian.PutUint32(corrupt[rawSizePos:rawSizePos+4], 10)
	newCRC := crc32.ChecksumIEEE(corrupt[:len(corrupt)-dirFooterSize])
	binary.LittleEndian.PutUint32(corrupt[len(corrupt)-12:len(corrupt)-8], newCRC)

	if _, err := DecodeDirectory(corrupt, newCRC, uint64(len(corrupt))); !errors.Is(err, ErrInvalidDir) {
		t.Fatalf("expected ErrInvalidDir, got %v", err)
	}
}
