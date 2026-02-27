package format

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

const chunkHeaderSize = 16

type ChunkRecordV1 struct {
	Codec    Codec
	Level    uint8
	RawSize  uint32
	CompSize uint32
	CRC32Raw uint32
	Payload  []byte
}

type ChunkHeaderV1 struct {
	Codec    Codec
	Level    uint8
	RawSize  uint32
	CompSize uint32
	CRC32Raw uint32
}

func EncodeChunkRecord(raw []byte, codec Codec, level uint8) ([]byte, uint32, error) {
	if len(raw) == 0 {
		return nil, 0, ErrInvalidChunk
	}
	comp, err := compressPayload(raw, codec, level)
	if err != nil {
		return nil, 0, err
	}
	crc := crc32.ChecksumIEEE(raw)
	out := make([]byte, chunkHeaderSize+len(comp))
	copy(out[0:2], MagicChunk[:])
	out[2] = byte(codec)
	out[3] = level
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(raw)))
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(comp)))
	binary.LittleEndian.PutUint32(out[12:16], crc)
	copy(out[chunkHeaderSize:], comp)
	return out, crc, nil
}

func ReadChunkRecordAt(r io.ReaderAt, offset int64) (ChunkRecordV1, error) {
	return ReadChunkRecordAtOpt(r, offset, true)
}

func ReadChunkRecordAtOpt(r io.ReaderAt, offset int64, verifyCRC bool) (ChunkRecordV1, error) {
	return ReadChunkRecordAtOptLimited(r, offset, verifyCRC, 0, 0)
}

func ReadChunkRecordAtOptLimited(r io.ReaderAt, offset int64, verifyCRC bool, maxRawSize, maxCompSize uint32) (ChunkRecordV1, error) {
	head, err := ReadChunkHeaderAt(r, offset)
	if err != nil {
		return ChunkRecordV1{}, err
	}
	if head.RawSize == 0 || head.CompSize == 0 {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	if maxRawSize > 0 && head.RawSize > maxRawSize {
		return ChunkRecordV1{}, ErrLimitExceeded
	}
	if maxCompSize > 0 && head.CompSize > maxCompSize {
		return ChunkRecordV1{}, ErrLimitExceeded
	}
	maxInt := uint64(int(^uint(0) >> 1))
	if uint64(head.CompSize) > maxInt {
		return ChunkRecordV1{}, ErrLimitExceeded
	}
	payload := make([]byte, int(head.CompSize))
	if _, err := r.ReadAt(payload, offset+chunkHeaderSize); err != nil {
		return ChunkRecordV1{}, err
	}
	if uint32(len(payload)) != head.CompSize {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	raw, err := decompressPayload(payload, head.Codec, head.RawSize)
	if err != nil {
		return ChunkRecordV1{}, err
	}
	if uint32(len(raw)) != head.RawSize {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	if verifyCRC && crc32.ChecksumIEEE(raw) != head.CRC32Raw {
		return ChunkRecordV1{}, ErrCRCMismatch
	}
	return ChunkRecordV1{
		Codec:    head.Codec,
		Level:    head.Level,
		RawSize:  head.RawSize,
		CompSize: head.CompSize,
		CRC32Raw: head.CRC32Raw,
		Payload:  raw,
	}, nil
}

func ReadChunkHeaderAt(r io.ReaderAt, offset int64) (ChunkHeaderV1, error) {
	var head [chunkHeaderSize]byte
	if _, err := r.ReadAt(head[:], offset); err != nil {
		return ChunkHeaderV1{}, err
	}
	if head[0] != MagicChunk[0] || head[1] != MagicChunk[1] {
		return ChunkHeaderV1{}, ErrInvalidChunk
	}
	return ChunkHeaderV1{
		Codec:    Codec(head[2]),
		Level:    head[3],
		RawSize:  binary.LittleEndian.Uint32(head[4:8]),
		CompSize: binary.LittleEndian.Uint32(head[8:12]),
		CRC32Raw: binary.LittleEndian.Uint32(head[12:16]),
	}, nil
}
