package format

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

func zstdCompress(src []byte, level int) ([]byte, error) {
	if len(src) == 0 {
		return nil, ErrInvalidChunk
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
	if err != nil {
		return nil, fmt.Errorf("fbx: zstd encoder init failed: %w", err)
	}
	defer enc.Close()
	out := enc.EncodeAll(src, make([]byte, 0, len(src)))
	return out, nil
}

func zstdDecompress(src []byte, expected int) ([]byte, error) {
	if len(src) == 0 || expected <= 0 {
		return nil, ErrInvalidChunk
	}
	limit := zstdDecodeMemoryLimit(expected)
	dec, err := zstd.NewReader(
		nil,
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(limit),
		zstd.WithDecoderMaxWindow(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("fbx: zstd decoder init failed: %w", err)
	}
	defer dec.Close()
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
	const (
		minLimit = 8 << 20   // 8 MiB floor for small chunks.
		maxLimit = 8 << 30   // 8 GiB hard cap.
		mult     = uint64(4) // headroom for frame/window overhead.
	)
	limit := uint64(expected) * mult
	if limit < minLimit {
		return minLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
