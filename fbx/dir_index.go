package fbx

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"sort"

	"github.com/pixfid/go-fbx/internal/format"
)

const (
	dirIndexDirHeaderSize    = 20
	dirIndexDirEntryHeadSize = 44
	dirIndexDirChunkRefSize  = 32
	dirIndexDirFooterSize    = 16
)

func buildDirIndexBlob(dirBlob []byte, dirOffset uint64, dirCRC uint32, generation uint64) ([]byte, error) {
	rows, entryCount, err := scanDirectoryEntriesForIndex(dirBlob)
	if err != nil {
		return nil, err
	}
	ranges := buildDirIndexHashRanges(rows)
	if len(rows) != int(entryCount) {
		return nil, ErrInvalidFormat
	}
	idx := format.DirIndexV1{
		Generation: generation,
		DirOffset:  dirOffset,
		DirSize:    uint64(len(dirBlob)),
		DirCRC32:   dirCRC,
		Entries:    rows,
		HashRanges: ranges,
	}
	blob, err := idx.MarshalBinary()
	if err != nil {
		return nil, mapErr(err)
	}
	return blob, nil
}

func buildDirIndexHashRanges(rows []format.DirIndexEntryV1) []format.DirIndexHashRangeV1 {
	if len(rows) == 0 {
		return nil
	}
	order := make([]int, len(rows))
	for i := range rows {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a := rows[order[i]]
		b := rows[order[j]]
		if a.PathHash64 != b.PathHash64 {
			return a.PathHash64 < b.PathHash64
		}
		return order[i] < order[j]
	})

	out := make([]format.DirIndexHashRangeV1, 0, len(rows))
	for i := 0; i < len(order); {
		j := i
		hash := rows[order[i]].PathHash64
		first := uint32(order[i])
		span := uint32(1)
		for j+1 < len(order) {
			next := order[j+1]
			if rows[next].PathHash64 != hash {
				break
			}
			if uint32(next) != first+span {
				break
			}
			span++
			j++
		}
		out = append(out, format.DirIndexHashRangeV1{
			PathHash64:      hash,
			FirstEntryIndex: first,
			EntrySpan:       span,
		})
		i = j + 1
	}
	return out
}

func scanDirectoryEntriesForIndex(dirBlob []byte) ([]format.DirIndexEntryV1, uint32, error) {
	entryCount, err := validateDirectoryBlobEnvelope(dirBlob, crc32.ChecksumIEEE(dirBlob[:len(dirBlob)-dirIndexDirFooterSize]), uint64(len(dirBlob)))
	if err != nil {
		return nil, 0, err
	}
	rows := make([]format.DirIndexEntryV1, 0, int(entryCount))
	cursor := uint64(dirIndexDirHeaderSize)
	for i := uint32(0); i < entryCount; i++ {
		entryHeadEnd, ok := addUint64Checked(cursor, dirIndexDirEntryHeadSize)
		if !ok || entryHeadEnd > uint64(len(dirBlob)-dirIndexDirFooterSize) {
			return nil, 0, ErrInvalidFormat
		}
		head := dirBlob[cursor:entryHeadEnd]
		pathHash := binary.LittleEndian.Uint64(head[0:8])
		chunkCount := binary.LittleEndian.Uint32(head[32:36])
		metaSize := binary.LittleEndian.Uint32(head[36:40])
		pathSize := binary.LittleEndian.Uint32(head[40:44])

		chunksBytes, ok := mulUint64Checked(uint64(chunkCount), dirIndexDirChunkRefSize)
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		entrySize, ok := addUint64Checked(dirIndexDirEntryHeadSize, chunksBytes)
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		entrySize, ok = addUint64Checked(entrySize, uint64(metaSize))
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		entrySize, ok = addUint64Checked(entrySize, uint64(pathSize))
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		entryEnd, ok := addUint64Checked(cursor, entrySize)
		if !ok || entryEnd > uint64(len(dirBlob)-dirIndexDirFooterSize) {
			return nil, 0, ErrInvalidFormat
		}
		pathOff, ok := addUint64Checked(cursor, dirIndexDirEntryHeadSize)
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		pathOff, ok = addUint64Checked(pathOff, chunksBytes)
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		pathOff, ok = addUint64Checked(pathOff, uint64(metaSize))
		if !ok {
			return nil, 0, ErrInvalidFormat
		}
		if cursor > uint64(^uint32(0)) || entrySize > uint64(^uint32(0)) || pathOff > uint64(^uint32(0)) {
			return nil, 0, ErrLimitExceeded
		}
		rows = append(rows, format.DirIndexEntryV1{
			DirEntryOffset: uint32(cursor),
			DirEntrySize:   uint32(entrySize),
			PathHash64:     pathHash,
			PathOffset:     uint32(pathOff),
			PathSize:       pathSize,
		})
		cursor = entryEnd
	}
	totalWithFooter, ok := addUint64Checked(cursor, dirIndexDirFooterSize)
	if !ok || totalWithFooter != uint64(len(dirBlob)) {
		return nil, 0, ErrInvalidFormat
	}
	return rows, entryCount, nil
}

func maybeReadDirIndexByHeader(f *os.File, h format.HeaderV1, fileSize uint64) (format.DirIndexV1, bool, error) {
	if h.Flags&format.HeaderFlagHasDirIndex == 0 {
		return format.DirIndexV1{}, false, nil
	}
	ptr := readHeaderDirIndexPointer(h)
	if ptr.offset == 0 || ptr.size == 0 || ptr.crc32 == 0 {
		return format.DirIndexV1{}, true, ErrInvalidFormat
	}
	if !regionWithinFile(ptr.offset, ptr.size, fileSize) {
		return format.DirIndexV1{}, true, ErrInvalidFormat
	}
	if ptr.size > maxDirectoryBlobSize {
		return format.DirIndexV1{}, true, ErrLimitExceeded
	}
	maxInt := uint64(int(^uint(0) >> 1))
	if ptr.size > maxInt {
		return format.DirIndexV1{}, true, ErrLimitExceeded
	}
	blob := make([]byte, int(ptr.size))
	if _, err := f.ReadAt(blob, int64(ptr.offset)); err != nil {
		return format.DirIndexV1{}, true, err
	}
	idx, err := format.DecodeDirIndexV1(blob, ptr.crc32)
	if err != nil {
		return format.DirIndexV1{}, true, mapErr(err)
	}
	if idx.DirOffset != h.DirOffset || idx.DirSize != h.DirSize || idx.DirCRC32 != h.DirCRC32 {
		return format.DirIndexV1{}, true, ErrInvalidFormat
	}
	gen := readHeaderGeneration(h)
	if gen > 0 && idx.Generation != gen {
		return format.DirIndexV1{}, true, ErrInvalidFormat
	}
	return idx, true, nil
}

func readEntriesByDirIndex(dirBlob []byte, h format.HeaderV1, idx format.DirIndexV1) (map[string]format.EntryV1, error) {
	entryCount, err := validateDirectoryBlobEnvelope(dirBlob, h.DirCRC32, h.DirSize)
	if err != nil {
		return nil, err
	}
	if int(entryCount) != len(idx.Entries) {
		return nil, ErrInvalidFormat
	}

	entries := make(map[string]format.EntryV1, len(idx.Entries))
	for _, row := range idx.Entries {
		e, err := format.DecodeDirectoryEntryAt(dirBlob, row.DirEntryOffset, row.DirEntrySize)
		if err != nil {
			return nil, mapErr(err)
		}
		if e.PathHash64 != row.PathHash64 || e.PathSize != row.PathSize {
			return nil, ErrInvalidFormat
		}
		pathEnd, ok := addUint64Checked(uint64(row.PathOffset), uint64(row.PathSize))
		if !ok || pathEnd > uint64(len(dirBlob)-dirIndexDirFooterSize) {
			return nil, ErrInvalidFormat
		}
		if pathEnd > uint64(int(^uint(0)>>1)) {
			return nil, ErrLimitExceeded
		}
		pathStart := int(row.PathOffset)
		pathStop := int(pathEnd)
		if string(dirBlob[pathStart:pathStop]) != e.Path {
			return nil, ErrInvalidFormat
		}
		if _, ok := entries[e.Path]; ok {
			return nil, ErrInvalidFormat
		}
		entries[e.Path] = e
	}
	return entries, nil
}

func validateDirectoryBlobEnvelope(dirBlob []byte, expectedCRC uint32, expectedSize uint64) (uint32, error) {
	if len(dirBlob) < dirIndexDirHeaderSize+dirIndexDirFooterSize {
		return 0, ErrInvalidFormat
	}
	if uint64(len(dirBlob)) != expectedSize {
		return 0, ErrInvalidFormat
	}
	if !bytes.Equal(dirBlob[:4], format.MagicDir[:]) {
		return 0, ErrInvalidFormat
	}
	footer := dirBlob[len(dirBlob)-dirIndexDirFooterSize:]
	if !bytes.Equal(footer[:4], format.MagicEnd[:]) {
		return 0, ErrInvalidFormat
	}
	crc := binary.LittleEndian.Uint32(footer[4:8])
	total := binary.LittleEndian.Uint64(footer[8:16])
	if crc != expectedCRC || total != expectedSize {
		return 0, ErrInvalidFormat
	}
	if crc32.ChecksumIEEE(dirBlob[:len(dirBlob)-dirIndexDirFooterSize]) != crc {
		return 0, ErrCRCMismatch
	}
	flags := binary.LittleEndian.Uint32(dirBlob[8:12])
	if flags != 0 {
		return 0, ErrInvalidFormat
	}
	return binary.LittleEndian.Uint32(dirBlob[4:8]), nil
}
