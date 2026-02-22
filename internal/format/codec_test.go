package format

import (
	"bytes"
	"errors"
	"testing"
)

func TestCodecDispatchAndUnsupported(t *testing.T) {
	payload := bytes.Repeat([]byte("abc"), 100)
	for _, codec := range []Codec{CodecStore, CodecZstd, CodecLZ4} {
		comp, err := compressPayload(payload, codec, 1)
		if err != nil {
			t.Fatalf("compress codec=%d: %v", codec, err)
		}
		raw, err := decompressPayload(comp, codec, uint32(len(payload)))
		if err != nil {
			t.Fatalf("decompress codec=%d: %v", codec, err)
		}
		if !bytes.Equal(raw, payload) {
			t.Fatalf("payload mismatch codec=%d", codec)
		}
	}
	if _, err := compressPayload(payload, Codec(77), 0); !errors.Is(err, ErrBadCodec) {
		t.Fatalf("expected ErrBadCodec, got %v", err)
	}
	if _, err := decompressPayload(payload, Codec(77), uint32(len(payload))); !errors.Is(err, ErrBadCodec) {
		t.Fatalf("expected ErrBadCodec, got %v", err)
	}
}
