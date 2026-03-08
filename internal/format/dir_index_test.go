package format

import (
	"hash/crc32"
	"reflect"
	"testing"
)

func TestDirIndexMarshalDecodeRoundTrip(t *testing.T) {
	idx := DirIndexV1{
		Generation: 3,
		DirOffset:  4096,
		DirSize:    8192,
		DirCRC32:   0x11223344,
		Entries: []DirIndexEntryV1{
			{DirEntryOffset: 20, DirEntrySize: 80, PathHash64: 1, PathOffset: 88, PathSize: 4},
			{DirEntryOffset: 100, DirEntrySize: 96, PathHash64: 2, PathOffset: 180, PathSize: 5},
			{DirEntryOffset: 196, DirEntrySize: 64, PathHash64: 2, PathOffset: 244, PathSize: 3},
		},
		HashRanges: []DirIndexHashRangeV1{
			{PathHash64: 1, FirstEntryIndex: 0, EntrySpan: 1},
			{PathHash64: 2, FirstEntryIndex: 1, EntrySpan: 2},
		},
	}

	blob, err := idx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := DecodeDirIndexV1(blob, crc32.ChecksumIEEE(blob))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(out, idx) {
		t.Fatalf("roundtrip mismatch\n got: %+v\nwant: %+v", out, idx)
	}
}

func TestDecodeDirIndexRejectsBadCRC(t *testing.T) {
	idx := DirIndexV1{
		Entries: []DirIndexEntryV1{{DirEntryOffset: 20, DirEntrySize: 80, PathHash64: 1, PathOffset: 88, PathSize: 4}},
		HashRanges: []DirIndexHashRangeV1{
			{PathHash64: 1, FirstEntryIndex: 0, EntrySpan: 1},
		},
	}
	blob, err := idx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := DecodeDirIndexV1(blob, crc32.ChecksumIEEE(blob)+1); err != ErrCRCMismatch {
		t.Fatalf("expected ErrCRCMismatch, got %v", err)
	}

	blob[60] ^= 0xFF
	if _, err := UnmarshalDirIndexV1(blob); err != ErrCRCMismatch {
		t.Fatalf("expected header ErrCRCMismatch, got %v", err)
	}
}

func TestDecodeDirIndexRejectsOverlappingHashRanges(t *testing.T) {
	idx := DirIndexV1{
		Entries: []DirIndexEntryV1{
			{DirEntryOffset: 20, DirEntrySize: 80, PathHash64: 1, PathOffset: 88, PathSize: 4},
			{DirEntryOffset: 100, DirEntrySize: 80, PathHash64: 1, PathOffset: 168, PathSize: 4},
		},
		HashRanges: []DirIndexHashRangeV1{
			{PathHash64: 1, FirstEntryIndex: 0, EntrySpan: 2},
			{PathHash64: 1, FirstEntryIndex: 1, EntrySpan: 1},
		},
	}
	blob, err := idx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := UnmarshalDirIndexV1(blob); err != ErrInvalidDir {
		t.Fatalf("expected ErrInvalidDir, got %v", err)
	}
}
