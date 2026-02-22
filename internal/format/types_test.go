package format

import "testing"

func TestCodecConstants(t *testing.T) {
	if CodecStore != 0 || CodecZstd != 1 || CodecLZ4 != 2 {
		t.Fatalf("unexpected codec constants")
	}
	var e EntryV1
	if e.Path != "" || e.FileSize != 0 {
		t.Fatalf("unexpected zero-value entry: %+v", e)
	}
}
