package format

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	zstdMinDecodeLimit = 8 << 20   // 8 MiB floor for small chunks.
	zstdMaxDecodeLimit = 8 << 30   // 8 GiB hard cap.
	zstdDecodeHeadroom = uint64(4) // headroom for frame/window overhead.
)

var (
	zstdEncMu   sync.Mutex
	zstdEncByLv = map[zstd.EncoderLevel]*zstd.Encoder{}

	zstdDecMu      sync.Mutex
	zstdDecByLimit = map[uint64]*zstd.Decoder{}
)

func zstdCompress(src []byte, level int) ([]byte, error) {
	if len(src) == 0 {
		return nil, ErrInvalidChunk
	}
	enc, err := zstdEncoderForLevel(level)
	if err != nil {
		return nil, fmt.Errorf("fbx: zstd encoder init failed: %w", err)
	}
	out := enc.EncodeAll(src, make([]byte, 0, len(src)))
	return out, nil
}

func zstdDecompress(src []byte, expected int) ([]byte, error) {
	if len(src) == 0 || expected <= 0 {
		return nil, ErrInvalidChunk
	}
	dec, err := zstdDecoderForLimit(zstdDecodeMemoryLimit(expected))
	if err != nil {
		return nil, fmt.Errorf("fbx: zstd decoder init failed: %w", err)
	}
	out, err := dec.DecodeAll(src, nil)
	if err != nil {
		return nil, fmt.Errorf("fbx: zstd decompress failed: %w", err)
	}
	if len(out) != expected {
		return nil, ErrInvalidChunk
	}
	return out, nil
}

func zstdDecodeMemoryLimit(expected int) uint64 {
	limit := uint64(expected) * zstdDecodeHeadroom
	if limit < zstdMinDecodeLimit {
		return zstdMinDecodeLimit
	}
	if limit > zstdMaxDecodeLimit {
		return zstdMaxDecodeLimit
	}
	return limit
}

func zstdEncoderForLevel(level int) (*zstd.Encoder, error) {
	lv := zstd.EncoderLevelFromZstd(level)
	zstdEncMu.Lock()
	defer zstdEncMu.Unlock()
	if enc := zstdEncByLv[lv]; enc != nil {
		return enc, nil
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(lv))
	if err != nil {
		return nil, err
	}
	zstdEncByLv[lv] = enc
	return enc, nil
}

func zstdDecoderForLimit(limit uint64) (*zstd.Decoder, error) {
	bucket := zstdDecoderLimitBucket(limit)
	zstdDecMu.Lock()
	defer zstdDecMu.Unlock()
	if dec := zstdDecByLimit[bucket]; dec != nil {
		return dec, nil
	}
	dec, err := zstd.NewReader(
		nil,
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(bucket),
		zstd.WithDecoderMaxWindow(bucket),
	)
	if err != nil {
		return nil, err
	}
	zstdDecByLimit[bucket] = dec
	return dec, nil
}

func zstdDecoderLimitBucket(limit uint64) uint64 {
	if limit <= zstdMinDecodeLimit {
		return zstdMinDecodeLimit
	}
	if limit >= zstdMaxDecodeLimit {
		return zstdMaxDecodeLimit
	}
	b := uint64(zstdMinDecodeLimit)
	for b < limit && b < zstdMaxDecodeLimit {
		b <<= 1
	}
	if b > zstdMaxDecodeLimit {
		return zstdMaxDecodeLimit
	}
	return b
}
