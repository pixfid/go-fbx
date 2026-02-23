package format

import (
	"bytes"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func TestMapLZ4Level(t *testing.T) {
	tests := []struct {
		in   int
		want lz4.CompressionLevel
	}{
		{in: -1, want: lz4.Fast},
		{in: 0, want: lz4.Fast},
		{in: 1, want: lz4.Level1},
		{in: 2, want: lz4.Level2},
		{in: 3, want: lz4.Level3},
		{in: 4, want: lz4.Level4},
		{in: 5, want: lz4.Level5},
		{in: 6, want: lz4.Level6},
		{in: 7, want: lz4.Level7},
		{in: 8, want: lz4.Level8},
		{in: 9, want: lz4.Level9},
		{in: 10, want: lz4.Level9},
		{in: 99, want: lz4.Level9},
	}
	for _, tc := range tests {
		got := mapLZ4Level(tc.in)
		if got != tc.want {
			t.Fatalf("mapLZ4Level(%d)=%v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLZ4CompressLevelRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("fb2-text-segment-"), 512)
	for _, level := range []uint8{0, 1, 3, 9, 15} {
		rec, _, err := EncodeChunkRecord(payload, CodecLZ4, level)
		if err != nil {
			t.Fatalf("encode level=%d: %v", level, err)
		}
		out, err := ReadChunkRecordAt(bytes.NewReader(rec), 0)
		if err != nil {
			t.Fatalf("decode level=%d: %v", level, err)
		}
		if out.Level != level {
			t.Fatalf("record level mismatch: got=%d want=%d", out.Level, level)
		}
		if !bytes.Equal(out.Payload, payload) {
			t.Fatalf("payload mismatch for level=%d", level)
		}
	}
}
