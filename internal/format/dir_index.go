package format

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"sort"
)

const (
	DirIndexVersion       uint16 = 1
	DirIndexHeaderSize    uint16 = 64
	DirIndexEntrySize     uint32 = 24
	DirIndexHashRangeSize uint32 = 16
)

var MagicDirIndex = [4]byte{'I', 'D', 'X', '1'}

type DirIndexEntryV1 struct {
	DirEntryOffset uint32
	DirEntrySize   uint32
	PathHash64     uint64
	PathOffset     uint32
	PathSize       uint32
}

type DirIndexHashRangeV1 struct {
	PathHash64      uint64
	FirstEntryIndex uint32
	EntrySpan       uint32
}

type DirIndexV1 struct {
	Flags      uint32
	Generation uint64
	DirOffset  uint64
	DirSize    uint64
	DirCRC32   uint32

	Entries    []DirIndexEntryV1
	HashRanges []DirIndexHashRangeV1
}

func (idx *DirIndexV1) MarshalBinary() ([]byte, error) {
	entryCount := len(idx.Entries)
	hashCount := len(idx.HashRanges)
	if uint64(entryCount) > uint64(^uint32(0)) || uint64(hashCount) > uint64(^uint32(0)) {
		return nil, ErrInvalidDir
	}
	entryBytes, ok := mulUint64(uint64(entryCount), uint64(DirIndexEntrySize))
	if !ok {
		return nil, ErrInvalidDir
	}
	hashBytes, ok := mulUint64(uint64(hashCount), uint64(DirIndexHashRangeSize))
	if !ok {
		return nil, ErrInvalidDir
	}
	total, ok := addUint64(uint64(DirIndexHeaderSize), entryBytes)
	if !ok {
		return nil, ErrInvalidDir
	}
	total, ok = addUint64(total, hashBytes)
	if !ok {
		return nil, ErrInvalidDir
	}
	maxInt := uint64(int(^uint(0) >> 1))
	if total > maxInt {
		return nil, ErrLimitExceeded
	}

	buf := make([]byte, int(total))
	copy(buf[0:4], MagicDirIndex[:])
	binary.LittleEndian.PutUint16(buf[4:6], DirIndexVersion)
	binary.LittleEndian.PutUint16(buf[6:8], DirIndexHeaderSize)
	binary.LittleEndian.PutUint32(buf[8:12], idx.Flags)
	binary.LittleEndian.PutUint32(buf[12:16], DirIndexEntrySize)
	binary.LittleEndian.PutUint32(buf[16:20], DirIndexHashRangeSize)
	binary.LittleEndian.PutUint64(buf[24:32], idx.Generation)
	binary.LittleEndian.PutUint64(buf[32:40], idx.DirOffset)
	binary.LittleEndian.PutUint64(buf[40:48], idx.DirSize)
	binary.LittleEndian.PutUint32(buf[48:52], idx.DirCRC32)
	binary.LittleEndian.PutUint32(buf[52:56], uint32(entryCount))
	binary.LittleEndian.PutUint32(buf[56:60], uint32(hashCount))

	off := int(DirIndexHeaderSize)
	for _, row := range idx.Entries {
		binary.LittleEndian.PutUint32(buf[off:off+4], row.DirEntryOffset)
		binary.LittleEndian.PutUint32(buf[off+4:off+8], row.DirEntrySize)
		binary.LittleEndian.PutUint64(buf[off+8:off+16], row.PathHash64)
		binary.LittleEndian.PutUint32(buf[off+16:off+20], row.PathOffset)
		binary.LittleEndian.PutUint32(buf[off+20:off+24], row.PathSize)
		off += int(DirIndexEntrySize)
	}
	for _, row := range idx.HashRanges {
		binary.LittleEndian.PutUint64(buf[off:off+8], row.PathHash64)
		binary.LittleEndian.PutUint32(buf[off+8:off+12], row.FirstEntryIndex)
		binary.LittleEndian.PutUint32(buf[off+12:off+16], row.EntrySpan)
		off += int(DirIndexHashRangeSize)
	}
	binary.LittleEndian.PutUint32(buf[60:64], crc32.ChecksumIEEE(buf[:60]))
	return buf, nil
}

func DecodeDirIndexV1(blob []byte, expectedCRC uint32) (DirIndexV1, error) {
	if crc32.ChecksumIEEE(blob) != expectedCRC {
		return DirIndexV1{}, ErrCRCMismatch
	}
	return UnmarshalDirIndexV1(blob)
}

func UnmarshalDirIndexV1(blob []byte) (DirIndexV1, error) {
	if len(blob) < int(DirIndexHeaderSize) {
		return DirIndexV1{}, ErrInvalidDir
	}
	if !bytes.Equal(blob[0:4], MagicDirIndex[:]) {
		return DirIndexV1{}, ErrInvalidDir
	}
	if binary.LittleEndian.Uint16(blob[4:6]) != DirIndexVersion {
		return DirIndexV1{}, ErrInvalidDir
	}
	if binary.LittleEndian.Uint16(blob[6:8]) != DirIndexHeaderSize {
		return DirIndexV1{}, ErrInvalidDir
	}
	if binary.LittleEndian.Uint32(blob[12:16]) != DirIndexEntrySize {
		return DirIndexV1{}, ErrInvalidDir
	}
	if binary.LittleEndian.Uint32(blob[16:20]) != DirIndexHashRangeSize {
		return DirIndexV1{}, ErrInvalidDir
	}
	if crc32.ChecksumIEEE(blob[:60]) != binary.LittleEndian.Uint32(blob[60:64]) {
		return DirIndexV1{}, ErrCRCMismatch
	}

	entryCount := binary.LittleEndian.Uint32(blob[52:56])
	hashCount := binary.LittleEndian.Uint32(blob[56:60])
	entryBytes, ok := mulUint64(uint64(entryCount), uint64(DirIndexEntrySize))
	if !ok {
		return DirIndexV1{}, ErrInvalidDir
	}
	hashBytes, ok := mulUint64(uint64(hashCount), uint64(DirIndexHashRangeSize))
	if !ok {
		return DirIndexV1{}, ErrInvalidDir
	}
	expectedLen, ok := addUint64(uint64(DirIndexHeaderSize), entryBytes)
	if !ok {
		return DirIndexV1{}, ErrInvalidDir
	}
	expectedLen, ok = addUint64(expectedLen, hashBytes)
	if !ok || expectedLen != uint64(len(blob)) {
		return DirIndexV1{}, ErrInvalidDir
	}

	idx := DirIndexV1{
		Flags:      binary.LittleEndian.Uint32(blob[8:12]),
		Generation: binary.LittleEndian.Uint64(blob[24:32]),
		DirOffset:  binary.LittleEndian.Uint64(blob[32:40]),
		DirSize:    binary.LittleEndian.Uint64(blob[40:48]),
		DirCRC32:   binary.LittleEndian.Uint32(blob[48:52]),
		Entries:    make([]DirIndexEntryV1, int(entryCount)),
		HashRanges: make([]DirIndexHashRangeV1, int(hashCount)),
	}

	off := int(DirIndexHeaderSize)
	for i := range idx.Entries {
		idx.Entries[i] = DirIndexEntryV1{
			DirEntryOffset: binary.LittleEndian.Uint32(blob[off : off+4]),
			DirEntrySize:   binary.LittleEndian.Uint32(blob[off+4 : off+8]),
			PathHash64:     binary.LittleEndian.Uint64(blob[off+8 : off+16]),
			PathOffset:     binary.LittleEndian.Uint32(blob[off+16 : off+20]),
			PathSize:       binary.LittleEndian.Uint32(blob[off+20 : off+24]),
		}
		off += int(DirIndexEntrySize)
	}
	for i := range idx.HashRanges {
		idx.HashRanges[i] = DirIndexHashRangeV1{
			PathHash64:      binary.LittleEndian.Uint64(blob[off : off+8]),
			FirstEntryIndex: binary.LittleEndian.Uint32(blob[off+8 : off+12]),
			EntrySpan:       binary.LittleEndian.Uint32(blob[off+12 : off+16]),
		}
		off += int(DirIndexHashRangeSize)
	}
	if err := validateDirIndexV1(idx); err != nil {
		return DirIndexV1{}, err
	}
	return idx, nil
}

func validateDirIndexV1(idx DirIndexV1) error {
	var prevEntryOff uint32
	for i, row := range idx.Entries {
		if row.DirEntrySize == 0 || row.PathSize == 0 {
			return ErrInvalidDir
		}
		if i > 0 && row.DirEntryOffset < prevEntryOff {
			return ErrInvalidDir
		}
		prevEntryOff = row.DirEntryOffset
	}

	type interval struct {
		first uint32
		last  uint32
	}
	used := make([]interval, 0, len(idx.HashRanges))
	var prevHash uint64
	for i, row := range idx.HashRanges {
		if row.EntrySpan == 0 {
			return ErrInvalidDir
		}
		end, ok := addUint64(uint64(row.FirstEntryIndex), uint64(row.EntrySpan))
		if !ok || end > uint64(len(idx.Entries)) {
			return ErrInvalidDir
		}
		if i > 0 && row.PathHash64 < prevHash {
			return ErrInvalidDir
		}
		prevHash = row.PathHash64

		for j := row.FirstEntryIndex; j < uint32(end); j++ {
			if idx.Entries[j].PathHash64 != row.PathHash64 {
				return ErrInvalidDir
			}
		}
		used = append(used, interval{first: row.FirstEntryIndex, last: uint32(end - 1)})
	}
	sort.Slice(used, func(i, j int) bool { return used[i].first < used[j].first })
	for i := 1; i < len(used); i++ {
		if used[i].first <= used[i-1].last {
			return ErrInvalidDir
		}
	}
	return nil
}
