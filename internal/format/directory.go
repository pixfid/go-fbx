package format

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"sort"
)

const dirFooterSize = 16
const dirHeaderSize = 20
const dirEntryHeadSize = 44
const dirChunkRefSize = 32

func EncodeDirectory(dir DirectoryV1) ([]byte, uint32, error) {
	if dir.Flags != 0 {
		return nil, 0, ErrInvalidDir
	}
	entries := make([]EntryV1, len(dir.Entries))
	copy(entries, dir.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	buf := bytes.NewBuffer(nil)
	_ = binary.Write(buf, binary.LittleEndian, MagicDir)
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(entries)))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, dir.BuildUnix)

	for _, e := range entries {
		if e.Path == "" {
			return nil, 0, ErrInvalidDir
		}
		e.PathHash64 = FNV1a64(e.Path)
		e.PathSize = uint32(len(e.Path))
		e.MetaSize = uint32(len(e.Meta))
		e.ChunkCount = uint32(len(e.Chunks))
		_ = binary.Write(buf, binary.LittleEndian, e.PathHash64)
		_ = binary.Write(buf, binary.LittleEndian, e.MTimeUnix)
		_ = binary.Write(buf, binary.LittleEndian, e.Mode)
		_ = binary.Write(buf, binary.LittleEndian, e.EntryFlags)
		_ = binary.Write(buf, binary.LittleEndian, e.FileSize)
		_ = binary.Write(buf, binary.LittleEndian, e.ChunkCount)
		_ = binary.Write(buf, binary.LittleEndian, e.MetaSize)
		_ = binary.Write(buf, binary.LittleEndian, e.PathSize)
		for _, c := range e.Chunks {
			_ = binary.Write(buf, binary.LittleEndian, c.ChunkOffset)
			_ = binary.Write(buf, binary.LittleEndian, c.RawOffset)
			_ = binary.Write(buf, binary.LittleEndian, c.RawSize)
			_ = binary.Write(buf, binary.LittleEndian, c.CompSize)
			_ = binary.Write(buf, binary.LittleEndian, c.CRC32Raw)
			_ = binary.Write(buf, binary.LittleEndian, uint32(0))
		}
		_, _ = buf.Write(e.Meta)
		_, _ = buf.WriteString(e.Path)
	}

	crc := crc32.ChecksumIEEE(buf.Bytes())
	_ = binary.Write(buf, binary.LittleEndian, MagicEnd)
	_ = binary.Write(buf, binary.LittleEndian, crc)
	_ = binary.Write(buf, binary.LittleEndian, uint64(buf.Len()+8))

	blob := buf.Bytes()
	if len(blob) < dirFooterSize {
		return nil, 0, ErrInvalidDir
	}
	return blob, crc, nil
}

func DecodeDirectory(blob []byte, expectedCRC uint32, expectedSize uint64) (DirectoryV1, error) {
	if len(blob) < dirHeaderSize+dirFooterSize {
		return DirectoryV1{}, ErrInvalidDir
	}
	if uint64(len(blob)) != expectedSize {
		return DirectoryV1{}, ErrInvalidDir
	}
	if !bytes.Equal(blob[:4], MagicDir[:]) {
		return DirectoryV1{}, ErrInvalidDir
	}
	footer := blob[len(blob)-dirFooterSize:]
	if !bytes.Equal(footer[:4], MagicEnd[:]) {
		return DirectoryV1{}, ErrInvalidDir
	}
	crc := binary.LittleEndian.Uint32(footer[4:8])
	total := binary.LittleEndian.Uint64(footer[8:16])
	if total != expectedSize || crc != expectedCRC {
		return DirectoryV1{}, ErrInvalidDir
	}
	calc := crc32.ChecksumIEEE(blob[:len(blob)-dirFooterSize])
	if calc != crc {
		return DirectoryV1{}, ErrCRCMismatch
	}

	r := bytes.NewReader(blob)
	var magic [4]byte
	var entryCount uint32
	var flags uint32
	var buildUnix uint64
	if err := binary.Read(r, binary.LittleEndian, &magic); err != nil {
		return DirectoryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &entryCount); err != nil {
		return DirectoryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &flags); err != nil {
		return DirectoryV1{}, ErrInvalidDir
	}
	if err := binary.Read(r, binary.LittleEndian, &buildUnix); err != nil {
		return DirectoryV1{}, ErrInvalidDir
	}
	if magic != MagicDir {
		return DirectoryV1{}, ErrInvalidDir
	}
	if flags != 0 {
		return DirectoryV1{}, ErrInvalidDir
	}

	maxInt := int(^uint(0) >> 1)
	if uint64(entryCount) > uint64(maxInt) {
		return DirectoryV1{}, ErrInvalidDir
	}
	maxEntriesByBlob := (len(blob) - dirHeaderSize - dirFooterSize) / dirEntryHeadSize
	if uint64(entryCount) > uint64(maxEntriesByBlob) {
		return DirectoryV1{}, ErrInvalidDir
	}

	d := DirectoryV1{EntryCount: entryCount, Flags: flags, BuildUnix: buildUnix, Entries: make([]EntryV1, 0, int(entryCount))}
	for i := 0; i < int(entryCount); i++ {
		if r.Len() < dirEntryHeadSize+dirFooterSize {
			return DirectoryV1{}, ErrInvalidDir
		}
		var e EntryV1
		if err := binary.Read(r, binary.LittleEndian, &e.PathHash64); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.MTimeUnix); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.Mode); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.EntryFlags); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.FileSize); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.ChunkCount); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.MetaSize); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if err := binary.Read(r, binary.LittleEndian, &e.PathSize); err != nil {
			return DirectoryV1{}, ErrInvalidDir
		}
		if e.ChunkCount == 0 && e.FileSize != 0 {
			return DirectoryV1{}, ErrInvalidDir
		}
		if uint64(e.ChunkCount) > uint64(maxInt) || uint64(e.MetaSize) > uint64(maxInt) || uint64(e.PathSize) > uint64(maxInt) {
			return DirectoryV1{}, ErrInvalidDir
		}
		chunksBytes, ok := mulUint64(uint64(e.ChunkCount), dirChunkRefSize)
		if !ok {
			return DirectoryV1{}, ErrInvalidDir
		}
		metaPathBytes, ok := addUint64(uint64(e.MetaSize), uint64(e.PathSize))
		if !ok {
			return DirectoryV1{}, ErrInvalidDir
		}
		needBytes, ok := addUint64(chunksBytes, metaPathBytes)
		if !ok {
			return DirectoryV1{}, ErrInvalidDir
		}
		needBytes, ok = addUint64(needBytes, dirFooterSize)
		if !ok || needBytes > uint64(r.Len()) {
			return DirectoryV1{}, ErrInvalidDir
		}

		e.Chunks = make([]ChunkRefV1, int(e.ChunkCount))
		var prevEnd uint64
		for j := range e.Chunks {
			if err := binary.Read(r, binary.LittleEndian, &e.Chunks[j].ChunkOffset); err != nil {
				return DirectoryV1{}, ErrInvalidDir
			}
			if err := binary.Read(r, binary.LittleEndian, &e.Chunks[j].RawOffset); err != nil {
				return DirectoryV1{}, ErrInvalidDir
			}
			if err := binary.Read(r, binary.LittleEndian, &e.Chunks[j].RawSize); err != nil {
				return DirectoryV1{}, ErrInvalidDir
			}
			if err := binary.Read(r, binary.LittleEndian, &e.Chunks[j].CompSize); err != nil {
				return DirectoryV1{}, ErrInvalidDir
			}
			if err := binary.Read(r, binary.LittleEndian, &e.Chunks[j].CRC32Raw); err != nil {
				return DirectoryV1{}, ErrInvalidDir
			}
			if err := binary.Read(r, binary.LittleEndian, &e.Chunks[j].Reserved); err != nil {
				return DirectoryV1{}, ErrInvalidDir
			}
			if e.Chunks[j].Reserved != 0 {
				return DirectoryV1{}, ErrInvalidDir
			}
			if j > 0 && e.Chunks[j].RawOffset < prevEnd {
				return DirectoryV1{}, ErrInvalidDir
			}
			prevEnd, ok = addUint64(e.Chunks[j].RawOffset, uint64(e.Chunks[j].RawSize))
			if !ok {
				return DirectoryV1{}, ErrInvalidDir
			}
		}
		if e.FileSize > 0 && len(e.Chunks) > 0 {
			last := e.Chunks[len(e.Chunks)-1]
			end, ok := addUint64(last.RawOffset, uint64(last.RawSize))
			if !ok || end != e.FileSize {
				return DirectoryV1{}, ErrInvalidDir
			}
		}
		e.Meta = make([]byte, int(e.MetaSize))
		if _, err := io.ReadFull(r, e.Meta); err != nil {
			return DirectoryV1{}, err
		}
		pathBytes := make([]byte, int(e.PathSize))
		if _, err := io.ReadFull(r, pathBytes); err != nil {
			return DirectoryV1{}, err
		}
		e.Path = string(pathBytes)
		if e.Path == "" {
			return DirectoryV1{}, ErrInvalidDir
		}
		if FNV1a64(e.Path) != e.PathHash64 {
			return DirectoryV1{}, ErrInvalidDir
		}
		d.Entries = append(d.Entries, e)
	}

	if r.Len() != dirFooterSize {
		return DirectoryV1{}, ErrInvalidDir
	}
	return d, nil
}

func addUint64(a, b uint64) (uint64, bool) {
	if a > ^uint64(0)-b {
		return 0, false
	}
	return a + b, true
}

func mulUint64(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > ^uint64(0)/b {
		return 0, false
	}
	return a * b, true
}
