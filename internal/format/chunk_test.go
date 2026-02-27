package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func TestChunkRoundTripAllCodecs(t *testing.T) {
	payload := bytes.Repeat([]byte("chunk-data-"), 64)
	for _, codec := range []Codec{CodecStore, CodecZstd, CodecLZ4} {
		rec, crc, err := EncodeChunkRecord(payload, codec, 1)
		if err != nil {
			t.Fatalf("encode codec=%d: %v", codec, err)
		}
		out, err := ReadChunkRecordAt(bytes.NewReader(rec), 0)
		if err != nil {
			t.Fatalf("read codec=%d: %v", codec, err)
		}
		if out.Codec != codec || out.CRC32Raw != crc || !bytes.Equal(out.Payload, payload) {
			t.Fatalf("roundtrip mismatch codec=%d", codec)
		}
	}
}

func TestChunkReadCRCValidation(t *testing.T) {
	rec, _, err := EncodeChunkRecord([]byte("hello"), CodecStore, 0)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec[16] ^= 0xFF
	if _, err := ReadChunkRecordAt(bytes.NewReader(rec), 0); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("expected ErrCRCMismatch, got %v", err)
	}
	if _, err := ReadChunkRecordAtOpt(bytes.NewReader(rec), 0, false); err != nil {
		t.Fatalf("crc disabled should pass, got %v", err)
	}
}

func TestChunkReadInvalidMagicAndSizes(t *testing.T) {
	rec, _, err := EncodeChunkRecord([]byte("x"), CodecStore, 0)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	bad := append([]byte(nil), rec...)
	bad[0] = 'X'
	if _, err := ReadChunkRecordAt(bytes.NewReader(bad), 0); err == nil {
		t.Fatalf("expected invalid magic error")
	}

	if _, _, err := EncodeChunkRecord(nil, CodecStore, 0); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("expected ErrInvalidChunk for empty payload, got %v", err)
	}

	if got := crc32.ChecksumIEEE([]byte("hello")); got == 0 {
		t.Fatalf("sanity crc check failed")
	}
}

func TestChunkReadHonorsLimitsBeforePayloadRead(t *testing.T) {
	var rec [chunkHeaderSize]byte
	rec[0] = MagicChunk[0]
	rec[1] = MagicChunk[1]
	rec[2] = byte(CodecStore)
	rec[3] = 0
	binary.LittleEndian.PutUint32(rec[4:8], 8)
	binary.LittleEndian.PutUint32(rec[8:12], 1<<20)
	binary.LittleEndian.PutUint32(rec[12:16], 0)

	if _, err := ReadChunkRecordAtOptLimited(bytes.NewReader(rec[:]), 0, false, 0, 16); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded on comp-size limit, got %v", err)
	}
}
