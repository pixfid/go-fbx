package format

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

func lz4Compress(src []byte, level int) ([]byte, error) {
	if len(src) == 0 {
		return nil, ErrInvalidChunk
	}
	bound := lz4.CompressBlockBound(len(src))
	if bound <= 0 {
		return nil, ErrBadCodec
	}
	out := make([]byte, bound)
	n, err := lz4CompressWithLevel(src, out, level)
	if err != nil {
		return nil, fmt.Errorf("fbx: lz4 compress failed: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("fbx: lz4 compress failed")
	}
	return out[:n], nil
}

func lz4CompressWithLevel(src, dst []byte, level int) (int, error) {
	if level <= 0 {
		return lz4.CompressBlock(src, dst, nil)
	}
	return lz4.CompressBlockHC(src, dst, mapLZ4Level(level), nil, nil)
}

func mapLZ4Level(level int) lz4.CompressionLevel {
	switch {
	case level <= 0:
		return lz4.Fast
	case level == 1:
		return lz4.Level1
	case level == 2:
		return lz4.Level2
	case level == 3:
		return lz4.Level3
	case level == 4:
		return lz4.Level4
	case level == 5:
		return lz4.Level5
	case level == 6:
		return lz4.Level6
	case level == 7:
		return lz4.Level7
	case level == 8:
		return lz4.Level8
	default:
		return lz4.Level9
	}
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
