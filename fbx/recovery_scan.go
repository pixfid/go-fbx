package fbx

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"

	"github.com/pixfid/go-fbx/internal/format"
)

const (
	recoveryScanBlockSize    = 1 << 20
	recoveryDirEntryHeadSize = 44
	recoveryChunkRefSize     = 32
	recoveryDirHeadSize      = 20
	recoveryDirFooterSize    = 16
)

func recoverHeaderFromDirectoryScan(f *os.File) (format.HeaderV1, error) {
	st, err := f.Stat()
	if err != nil {
		return format.HeaderV1{}, err
	}
	fileSize := uint64(st.Size())
	if fileSize < recoveryDirHeadSize+recoveryDirFooterSize {
		return format.HeaderV1{}, ErrInvalidFormat
	}

	var (
		bestHeader format.HeaderV1
		bestBuild  uint64
		bestOffset uint64
		found      bool
		tail       []byte
	)

	for off := uint64(0); off < fileSize; {
		readSize := uint64(recoveryScanBlockSize)
		if remain := fileSize - off; remain < readSize {
			readSize = remain
		}
		block := make([]byte, readSize)
		n, rerr := f.ReadAt(block, int64(off))
		if rerr != nil && rerr != io.EOF {
			return format.HeaderV1{}, rerr
		}
		if n == 0 {
			break
		}
		block = block[:n]

		window := make([]byte, 0, len(tail)+len(block))
		window = append(window, tail...)
		window = append(window, block...)
		baseOffset := off - uint64(len(tail))

		searchFrom := 0
		for {
			idx := bytes.Index(window[searchFrom:], format.MagicDir[:])
			if idx < 0 {
				break
			}
			pos := searchFrom + idx
			dirOffset := baseOffset + uint64(pos)
			h, buildUnix, herr := buildHeaderFromDirectoryAt(f, dirOffset, fileSize)
			if herr == nil {
				if !found || buildUnix > bestBuild || (buildUnix == bestBuild && dirOffset > bestOffset) {
					bestHeader = h
					bestBuild = buildUnix
					bestOffset = dirOffset
					found = true
				}
			}
			searchFrom = pos + 1
		}

		tailKeep := len(format.MagicDir) - 1
		if len(window) <= tailKeep {
			tail = append(tail[:0], window...)
		} else {
			tail = append(tail[:0], window[len(window)-tailKeep:]...)
		}
		off += uint64(n)
	}

	if !found {
		return format.HeaderV1{}, ErrInvalidFormat
	}
	return bestHeader, nil
}

func buildHeaderFromDirectoryAt(f *os.File, dirOffset, fileSize uint64) (format.HeaderV1, uint64, error) {
	dir, dirSize, dirCRC, err := readDirectoryCandidateAt(f, dirOffset, fileSize)
	if err != nil {
		return format.HeaderV1{}, 0, err
	}
	if err := validateDirectoryChunkOffsets(dir, fileSize); err != nil {
		return format.HeaderV1{}, 0, err
	}

	h := format.HeaderV1{
		Magic:       format.MagicHeader,
		Version:     format.VersionV1,
		HeaderSize:  format.HeaderSize,
		CreatedUnix: dir.BuildUnix,
		DirOffset:   dirOffset,
		DirSize:     dirSize,
		DirCRC32:    dirCRC,
	}
	if err := validateHeaderDirectory(f, h); err != nil {
		return format.HeaderV1{}, 0, err
	}
	return h, dir.BuildUnix, nil
}

func readDirectoryCandidateAt(f *os.File, dirOffset, fileSize uint64) (format.DirectoryV1, uint64, uint32, error) {
	minSize, ok := addUint64Checked(recoveryDirHeadSize, recoveryDirFooterSize)
	if !ok || !regionWithinFile(dirOffset, minSize, fileSize) {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}

	var head [recoveryDirHeadSize]byte
	if _, err := f.ReadAt(head[:], int64(dirOffset)); err != nil {
		return format.DirectoryV1{}, 0, 0, err
	}
	if !bytes.Equal(head[0:4], format.MagicDir[:]) {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}
	entryCount := binary.LittleEndian.Uint32(head[4:8])
	flags := binary.LittleEndian.Uint32(head[8:12])
	if flags != 0 {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}
	maxEntriesBySize := (fileSize - dirOffset - recoveryDirHeadSize - recoveryDirFooterSize) / recoveryDirEntryHeadSize
	if uint64(entryCount) > maxEntriesBySize {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}

	cursor := uint64(recoveryDirHeadSize)
	for i := uint32(0); i < entryCount; i++ {
		entryHeadEnd, ok := addUint64Checked(cursor, recoveryDirEntryHeadSize)
		if !ok || !regionWithinFile(dirOffset, entryHeadEnd, fileSize) {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
		var entryHead [recoveryDirEntryHeadSize]byte
		entryOff, ok := addUint64Checked(dirOffset, cursor)
		if !ok {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
		if _, err := f.ReadAt(entryHead[:], int64(entryOff)); err != nil {
			return format.DirectoryV1{}, 0, 0, err
		}
		chunkCount := binary.LittleEndian.Uint32(entryHead[32:36])
		metaSize := binary.LittleEndian.Uint32(entryHead[36:40])
		pathSize := binary.LittleEndian.Uint32(entryHead[40:44])

		cursor = entryHeadEnd
		chunksBytes, ok := mulUint64Checked(uint64(chunkCount), recoveryChunkRefSize)
		if !ok {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
		cursor, ok = addUint64Checked(cursor, chunksBytes)
		if !ok {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
		metaPathBytes, ok := addUint64Checked(uint64(metaSize), uint64(pathSize))
		if !ok {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
		cursor, ok = addUint64Checked(cursor, metaPathBytes)
		if !ok {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
		withFooter, ok := addUint64Checked(cursor, recoveryDirFooterSize)
		if !ok || !regionWithinFile(dirOffset, withFooter, fileSize) {
			return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
		}
	}

	dirSize, ok := addUint64Checked(cursor, recoveryDirFooterSize)
	if !ok || !regionWithinFile(dirOffset, dirSize, fileSize) {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}
	if dirSize > maxDirectoryBlobSize {
		return format.DirectoryV1{}, 0, 0, ErrLimitExceeded
	}
	maxInt := uint64(int(^uint(0) >> 1))
	if dirSize > maxInt {
		return format.DirectoryV1{}, 0, 0, ErrLimitExceeded
	}
	blob := make([]byte, int(dirSize))
	if _, err := f.ReadAt(blob, int64(dirOffset)); err != nil {
		return format.DirectoryV1{}, 0, 0, err
	}

	footer := blob[len(blob)-recoveryDirFooterSize:]
	if !bytes.Equal(footer[0:4], format.MagicEnd[:]) {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}
	dirCRC := binary.LittleEndian.Uint32(footer[4:8])
	total := binary.LittleEndian.Uint64(footer[8:16])
	if total != dirSize {
		return format.DirectoryV1{}, 0, 0, ErrInvalidFormat
	}
	dir, err := format.DecodeDirectory(blob, dirCRC, dirSize)
	if err != nil {
		return format.DirectoryV1{}, 0, 0, err
	}
	return dir, dirSize, dirCRC, nil
}

func validateDirectoryChunkOffsets(dir format.DirectoryV1, fileSize uint64) error {
	for _, e := range dir.Entries {
		for _, ref := range e.Chunks {
			chunkEnd := ref.ChunkOffset + 16 + uint64(ref.CompSize)
			if chunkEnd < ref.ChunkOffset || chunkEnd > fileSize {
				return ErrInvalidFormat
			}
		}
	}
	return nil
}
