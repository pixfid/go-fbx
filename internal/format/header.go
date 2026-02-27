package format

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	HeaderSize = 128
	VersionV1  = 1
)

const (
	HeaderFlagHasJournal uint32 = 1 << 0
	HeaderFlagHasBackup  uint32 = 1 << 1
)

var (
	MagicHeader = [4]byte{'F', 'B', 'X', 'C'}
	MagicDir    = [4]byte{'D', 'I', 'R', '1'}
	MagicEnd    = [4]byte{'E', 'N', 'D', '1'}
	MagicChunk  = [2]byte{'C', 'K'}
)

var (
	ErrInvalidHeader = errors.New("fbx: invalid header")
	ErrInvalidDir    = errors.New("fbx: invalid directory")
	ErrInvalidChunk  = errors.New("fbx: invalid chunk")
	ErrCRCMismatch   = errors.New("fbx: crc mismatch")
	ErrBadCodec      = errors.New("fbx: unsupported codec")
	ErrLimitExceeded = errors.New("fbx: limit exceeded")
)

type HeaderV1 struct {
	Magic         [4]byte
	Version       uint16
	HeaderSize    uint16
	Flags         uint32
	UUID          [16]byte
	CreatedUnix   uint64
	DirOffset     uint64
	DirSize       uint64
	DirCRC32      uint32
	JournalOffset uint64
	JournalSize   uint64
	Reserved      [56]byte
}

func (h *HeaderV1) MarshalBinary() ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, HeaderSize))
	_ = binary.Write(buf, binary.LittleEndian, h.Magic)
	_ = binary.Write(buf, binary.LittleEndian, h.Version)
	_ = binary.Write(buf, binary.LittleEndian, h.HeaderSize)
	_ = binary.Write(buf, binary.LittleEndian, h.Flags)
	_ = binary.Write(buf, binary.LittleEndian, h.UUID)
	_ = binary.Write(buf, binary.LittleEndian, h.CreatedUnix)
	_ = binary.Write(buf, binary.LittleEndian, h.DirOffset)
	_ = binary.Write(buf, binary.LittleEndian, h.DirSize)
	_ = binary.Write(buf, binary.LittleEndian, h.DirCRC32)
	_ = binary.Write(buf, binary.LittleEndian, h.JournalOffset)
	_ = binary.Write(buf, binary.LittleEndian, h.JournalSize)
	_ = binary.Write(buf, binary.LittleEndian, h.Reserved)
	if buf.Len() != HeaderSize {
		return nil, ErrInvalidHeader
	}
	return buf.Bytes(), nil
}

func UnmarshalHeaderV1(b []byte) (HeaderV1, error) {
	if len(b) != HeaderSize {
		return HeaderV1{}, ErrInvalidHeader
	}
	var h HeaderV1
	r := bytes.NewReader(b)
	_ = binary.Read(r, binary.LittleEndian, &h.Magic)
	_ = binary.Read(r, binary.LittleEndian, &h.Version)
	_ = binary.Read(r, binary.LittleEndian, &h.HeaderSize)
	_ = binary.Read(r, binary.LittleEndian, &h.Flags)
	_ = binary.Read(r, binary.LittleEndian, &h.UUID)
	_ = binary.Read(r, binary.LittleEndian, &h.CreatedUnix)
	_ = binary.Read(r, binary.LittleEndian, &h.DirOffset)
	_ = binary.Read(r, binary.LittleEndian, &h.DirSize)
	_ = binary.Read(r, binary.LittleEndian, &h.DirCRC32)
	_ = binary.Read(r, binary.LittleEndian, &h.JournalOffset)
	_ = binary.Read(r, binary.LittleEndian, &h.JournalSize)
	_ = binary.Read(r, binary.LittleEndian, &h.Reserved)
	if h.Magic != MagicHeader || h.Version != VersionV1 || h.HeaderSize != HeaderSize {
		return HeaderV1{}, ErrInvalidHeader
	}
	return h, nil
}
