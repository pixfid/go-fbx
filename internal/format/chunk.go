package format

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

type ChunkRecordV1 struct {
	Codec    Codec
	Level    uint8
	RawSize  uint32
	CompSize uint32
	CRC32Raw uint32
	Payload  []byte
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
	out := make([]byte, 16+len(comp))
	copy(out[0:2], MagicChunk[:])
	out[2] = byte(codec)
	out[3] = level
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(raw)))
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(comp)))
	binary.LittleEndian.PutUint32(out[12:16], crc)
	copy(out[16:], comp)
	return out, crc, nil
}

func ReadChunkRecordAt(r io.ReaderAt, offset int64) (ChunkRecordV1, error) {
	return ReadChunkRecordAtOpt(r, offset, true)
}

func ReadChunkRecordAtOpt(r io.ReaderAt, offset int64, verifyCRC bool) (ChunkRecordV1, error) {
	head := make([]byte, 16)
	if _, err := r.ReadAt(head, offset); err != nil {
		return ChunkRecordV1{}, err
	}
	if head[0] != 'C' || head[1] != 'K' {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	codec := Codec(head[2])
	level := head[3]
	rawSize := binary.LittleEndian.Uint32(head[4:8])
	compSize := binary.LittleEndian.Uint32(head[8:12])
	crc := binary.LittleEndian.Uint32(head[12:16])
	if rawSize == 0 || compSize == 0 {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	payload := make([]byte, compSize)
	if _, err := r.ReadAt(payload, offset+16); err != nil {
		return ChunkRecordV1{}, err
	}
	if uint32(len(payload)) != compSize {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	raw, err := decompressPayload(payload, codec, rawSize)
	if err != nil {
		return ChunkRecordV1{}, err
	}
	if uint32(len(raw)) != rawSize {
		return ChunkRecordV1{}, ErrInvalidChunk
	}
	if verifyCRC && crc32.ChecksumIEEE(raw) != crc {
		return ChunkRecordV1{}, ErrCRCMismatch
	}
	return ChunkRecordV1{
		Codec:    codec,
		Level:    level,
		RawSize:  rawSize,
		CompSize: compSize,
		CRC32Raw: crc,
		Payload:  raw,
	}, nil
}
