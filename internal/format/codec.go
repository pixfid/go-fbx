package format

func compressPayload(raw []byte, codec Codec, level uint8) ([]byte, error) {
	switch codec {
	case CodecStore:
		return append([]byte(nil), raw...), nil
	case CodecZstd:
		return zstdCompress(raw, int(level))
	case CodecLZ4:
		return lz4Compress(raw, int(level))
	default:
		return nil, ErrBadCodec
	}
}

func decompressPayload(comp []byte, codec Codec, expectedRawSize uint32) ([]byte, error) {
	switch codec {
	case CodecStore:
		return append([]byte(nil), comp...), nil
	case CodecZstd:
		return zstdDecompress(comp, int(expectedRawSize))
	case CodecLZ4:
		return lz4Decompress(comp, int(expectedRawSize))
	default:
		return nil, ErrBadCodec
	}
}
