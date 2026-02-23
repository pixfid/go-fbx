package format

import "testing"

func TestZstdEncoderForLevelReuse(t *testing.T) {
	e1, err := zstdEncoderForLevel(3)
	if err != nil {
		t.Fatalf("encoder level=3: %v", err)
	}
	e2, err := zstdEncoderForLevel(4)
	if err != nil {
		t.Fatalf("encoder level=4: %v", err)
	}
	if e1 != e2 {
		t.Fatalf("expected same cached encoder for zstd levels 3 and 4")
	}
}

func TestZstdDecoderLimitBucket(t *testing.T) {
	tests := []struct {
		in   uint64
		want uint64
	}{
		{in: 1, want: zstdMinDecodeLimit},
		{in: zstdMinDecodeLimit, want: zstdMinDecodeLimit},
		{in: zstdMinDecodeLimit + 1, want: zstdMinDecodeLimit << 1},
		{in: 20 << 20, want: 32 << 20},
		{in: zstdMaxDecodeLimit, want: zstdMaxDecodeLimit},
		{in: zstdMaxDecodeLimit + 1, want: zstdMaxDecodeLimit},
	}
	for _, tc := range tests {
		got := zstdDecoderLimitBucket(tc.in)
		if got != tc.want {
			t.Fatalf("bucket(%d)=%d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestZstdDecoderForLimitReuse(t *testing.T) {
	d1, err := zstdDecoderForLimit(9 << 20)
	if err != nil {
		t.Fatalf("decoder 9MiB: %v", err)
	}
	d2, err := zstdDecoderForLimit(15 << 20)
	if err != nil {
		t.Fatalf("decoder 15MiB: %v", err)
	}
	if d1 != d2 {
		t.Fatalf("expected same cached decoder for same bucket")
	}

	d3, err := zstdDecoderForLimit(40 << 20)
	if err != nil {
		t.Fatalf("decoder 40MiB: %v", err)
	}
	if d1 == d3 {
		t.Fatalf("expected different decoder for different bucket")
	}
}
