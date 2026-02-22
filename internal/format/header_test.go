package format

import "testing"

func TestHeaderMarshalUnmarshal(t *testing.T) {
	h := HeaderV1{Magic: MagicHeader, Version: VersionV1, HeaderSize: HeaderSize, DirOffset: 128, DirSize: 64, DirCRC32: 123}
	b, err := h.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalHeaderV1(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.DirOffset != h.DirOffset || out.DirSize != h.DirSize || out.DirCRC32 != h.DirCRC32 {
		t.Fatalf("header mismatch: got=%+v want=%+v", out, h)
	}

	b[0] = 'X'
	if _, err := UnmarshalHeaderV1(b); err == nil {
		t.Fatalf("expected invalid header error")
	}
}
