package format

import "testing"

func TestDecodeDirectoryEntryAt(t *testing.T) {
	dir := DirectoryV1{Entries: []EntryV1{{Path: "a.fb2", Meta: []byte(`{"k":1}`), FileSize: 0}}}
	blob, _, err := EncodeDirectory(dir)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	entryOff := uint32(dirHeaderSize)
	entrySize := uint32(len(blob) - dirHeaderSize - dirFooterSize)
	e, err := DecodeDirectoryEntryAt(blob, entryOff, entrySize)
	if err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if e.Path != "a.fb2" {
		t.Fatalf("path mismatch: %q", e.Path)
	}
	if string(e.Meta) != `{"k":1}` {
		t.Fatalf("meta mismatch: %q", string(e.Meta))
	}

	if _, err := DecodeDirectoryEntryAt(blob, entryOff, entrySize+1); err == nil {
		t.Fatalf("expected error for invalid entry size")
	}
}
