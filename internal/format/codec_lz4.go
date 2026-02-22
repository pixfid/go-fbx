package format

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

func lz4Compress(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, ErrInvalidChunk
	}
	bound := lz4.CompressBlockBound(len(src))
	if bound <= 0 {
		return nil, ErrBadCodec
	}
	out := make([]byte, bound)
	n, err := lz4.CompressBlock(src, out, nil)
	if err != nil {
		return nil, fmt.Errorf("fbx: lz4 compress failed: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("fbx: lz4 compress failed")
	}
	return out[:n], nil
}

func lz4Decompress(src []byte, expected int) ([]byte, error) {
	if len(src) == 0 || expected <= 0 {
		return nil, ErrInvalidChunk
	}
	out := make([]byte, expected)
	n, err := lz4.UncompressBlock(src, out)
	if err != nil {
		return nil, fmt.Errorf("fbx: lz4 decompress failed: %w", err)
	}
	if n != expected {
		return nil, ErrInvalidChunk
	}
	return out, nil
}
