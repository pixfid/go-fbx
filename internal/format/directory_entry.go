package format

import (
	"bytes"
	"encoding/binary"
	"io"
)

// DecodeDirectoryEntryAt decodes one directory entry payload from DIR1 blob.
// entryOff and entrySize are relative to the start of directory blob.
func DecodeDirectoryEntryAt(blob []byte, entryOff uint32, entrySize uint32) (EntryV1, error) {
	if len(blob) < dirHeaderSize+dirFooterSize {
		return EntryV1{}, ErrInvalidDir
	}
	start := uint64(entryOff)
	size := uint64(entrySize)
	if size < dirEntryHeadSize {
		return EntryV1{}, ErrInvalidDir
	}
	if start < dirHeaderSize {
		return EntryV1{}, ErrInvalidDir
	}
	end, ok := addUint64(start, size)
	if !ok {
		return EntryV1{}, ErrInvalidDir
	}
	if end > uint64(len(blob)-dirFooterSize) {
		return EntryV1{}, ErrInvalidDir
	}
	if end > uint64(int(^uint(0)>>1)) {
		return EntryV1{}, ErrInvalidDir
	}
	entryBlob := blob[start:end]

	r := bytes.NewReader(entryBlob)
	var e EntryV1
	if err := binary.Read(r, binary.LittleEndian, &e.PathHash64); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.MTimeUnix); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.Mode); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.EntryFlags); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.FileSize); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.ChunkCount); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.MetaSize); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &e.PathSize); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if e.ChunkCount == 0 && e.FileSize != 0 {
		return EntryV1{}, ErrInvalidDir
	}

	maxInt := uint64(int(^uint(0) >> 1))
	if uint64(e.ChunkCount) > maxInt || uint64(e.MetaSize) > maxInt || uint64(e.PathSize) > maxInt {
		return EntryV1{}, ErrInvalidDir
	}
	chunksBytes, ok := mulUint64(uint64(e.ChunkCount), dirChunkRefSize)
	if !ok {
		return EntryV1{}, ErrInvalidDir
	}
	metaPathBytes, ok := addUint64(uint64(e.MetaSize), uint64(e.PathSize))
	if !ok {
		return EntryV1{}, ErrInvalidDir
	}
	need, ok := addUint64(dirEntryHeadSize, chunksBytes)
	if !ok {
		return EntryV1{}, ErrInvalidDir
	}
	need, ok = addUint64(need, metaPathBytes)
	if !ok || need != size {
		return EntryV1{}, ErrInvalidDir
	}

	e.Chunks = make([]ChunkRefV1, int(e.ChunkCount))
	var prevEnd uint64
	for i := range e.Chunks {
		if err := binary.Read(r, binary.LittleEndian, &e.Chunks[i].ChunkOffset); err != nil {
			return EntryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.Chunks[i].RawOffset); err != nil {
			return EntryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.Chunks[i].RawSize); err != nil {
			return EntryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.Chunks[i].CompSize); err != nil {
			return EntryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.Chunks[i].CRC32Raw); err != nil {
			return EntryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.Chunks[i].Reserved); err != nil {
			return EntryV1{}, ErrInvalidDir
		}
		if e.Chunks[i].Reserved != 0 {
			return EntryV1{}, ErrInvalidDir
		}
		if i > 0 && e.Chunks[i].RawOffset < prevEnd {
			return EntryV1{}, ErrInvalidDir
		}
		prevEnd, ok = addUint64(e.Chunks[i].RawOffset, uint64(e.Chunks[i].RawSize))
		if !ok {
			return EntryV1{}, ErrInvalidDir
		}
	}
	if e.FileSize > 0 && len(e.Chunks) > 0 {
		last := e.Chunks[len(e.Chunks)-1]
		end, ok := addUint64(last.RawOffset, uint64(last.RawSize))
		if !ok || end != e.FileSize {
			return EntryV1{}, ErrInvalidDir
		}
	}

	e.Meta = make([]byte, int(e.MetaSize))
	if _, err := io.ReadFull(r, e.Meta); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	pathBytes := make([]byte, int(e.PathSize))
	if _, err := io.ReadFull(r, pathBytes); err != nil {
		return EntryV1{}, ErrInvalidDir
	}
	if r.Len() != 0 {
		return EntryV1{}, ErrInvalidDir
	}
	e.Path = string(pathBytes)
	if e.Path == "" {
		return EntryV1{}, ErrInvalidDir
	}
	if FNV1a64(e.Path) != e.PathHash64 {
		return EntryV1{}, ErrInvalidDir
	}
	return e, nil
}
