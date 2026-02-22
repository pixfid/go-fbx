package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sort"
)

const dirFooterSize = 16

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
	if len(blob) < 20+dirFooterSize {
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
		return DirectoryV1{}, errors.New("fbx: crc mismatch")
	}

	r := bytes.NewReader(blob)
	var magic [4]byte
	var entryCount uint32
	var flags uint32
	var buildUnix uint64
	_ = binary.Read(r, binary.LittleEndian, &magic)
	_ = binary.Read(r, binary.LittleEndian, &entryCount)
	_ = binary.Read(r, binary.LittleEndian, &flags)
	_ = binary.Read(r, binary.LittleEndian, &buildUnix)
	if flags != 0 {
		return DirectoryV1{}, ErrInvalidDir
	}

	d := DirectoryV1{EntryCount: entryCount, Flags: flags, BuildUnix: buildUnix, Entries: make([]EntryV1, 0, entryCount)}
	for i := 0; i < int(entryCount); i++ {
		var e EntryV1
		_ = binary.Read(r, binary.LittleEndian, &e.PathHash64)
		_ = binary.Read(r, binary.LittleEndian, &e.MTimeUnix)
		_ = binary.Read(r, binary.LittleEndian, &e.Mode)
		_ = binary.Read(r, binary.LittleEndian, &e.EntryFlags)
		_ = binary.Read(r, binary.LittleEndian, &e.FileSize)
		_ = binary.Read(r, binary.LittleEndian, &e.ChunkCount)
		_ = binary.Read(r, binary.LittleEndian, &e.MetaSize)
		_ = binary.Read(r, binary.LittleEndian, &e.PathSize)
		if e.ChunkCount == 0 && e.FileSize != 0 {
			return DirectoryV1{}, ErrInvalidDir
		}
		e.Chunks = make([]ChunkRefV1, e.ChunkCount)
		var prevEnd uint64
		for j := range e.Chunks {
			_ = binary.Read(r, binary.LittleEndian, &e.Chunks[j].ChunkOffset)
			_ = binary.Read(r, binary.LittleEndian, &e.Chunks[j].RawOffset)
			_ = binary.Read(r, binary.LittleEndian, &e.Chunks[j].RawSize)
			_ = binary.Read(r, binary.LittleEndian, &e.Chunks[j].CompSize)
			_ = binary.Read(r, binary.LittleEndian, &e.Chunks[j].CRC32Raw)
			_ = binary.Read(r, binary.LittleEndian, &e.Chunks[j].Reserved)
			if e.Chunks[j].Reserved != 0 {
				return DirectoryV1{}, ErrInvalidDir
			}
			if j > 0 && e.Chunks[j].RawOffset < prevEnd {
				return DirectoryV1{}, ErrInvalidDir
			}
			prevEnd = e.Chunks[j].RawOffset + uint64(e.Chunks[j].RawSize)
		}
		if e.FileSize > 0 && len(e.Chunks) > 0 {
			last := e.Chunks[len(e.Chunks)-1]
			if last.RawOffset+uint64(last.RawSize) != e.FileSize {
				return DirectoryV1{}, ErrInvalidDir
			}
		}
		e.Meta = make([]byte, e.MetaSize)
		if _, err := r.Read(e.Meta); err != nil {
			return DirectoryV1{}, err
		}
		pathBytes := make([]byte, e.PathSize)
		if _, err := r.Read(pathBytes); err != nil {
			return DirectoryV1{}, err
		}
		e.Path = string(pathBytes)
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
