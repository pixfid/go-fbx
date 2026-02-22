package fbx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"go-fbx/internal/format"
)

var journalMagic = [4]byte{'J', 'N', 'L', '1'}
var backupMagic = [4]byte{'B', 'K', 'P', '1'}

const (
	journalRecordSize  = 4 + 8 + format.HeaderSize + 4 + 4
	journalSearchBytes = 8 << 20
	fixedBackupOffset  = int64(format.HeaderSize)
)

func buildJournalRecord(headerBytes []byte, ts uint64) ([]byte, error) {
	return buildHeaderRecord(journalMagic, headerBytes, ts)
}

func buildBackupRecord(headerBytes []byte, ts uint64) ([]byte, error) {
	return buildHeaderRecord(backupMagic, headerBytes, ts)
}

func buildHeaderRecord(magic [4]byte, headerBytes []byte, ts uint64) ([]byte, error) {
	if len(headerBytes) != format.HeaderSize {
		return nil, ErrInvalidFormat
	}
	headerCRC := crc32.ChecksumIEEE(headerBytes)
	buf := bytes.NewBuffer(make([]byte, 0, journalRecordSize))
	_ = binary.Write(buf, binary.LittleEndian, magic)
	_ = binary.Write(buf, binary.LittleEndian, ts)
	_, _ = buf.Write(headerBytes)
	_ = binary.Write(buf, binary.LittleEndian, headerCRC)
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))

	b := buf.Bytes()
	if len(b) != journalRecordSize {
		return nil, ErrInvalidFormat
	}
	recordCRC := crc32.ChecksumIEEE(b[:len(b)-4])
	binary.LittleEndian.PutUint32(b[len(b)-4:], recordCRC)
	return b, nil
}

func parseJournalRecord(b []byte) (headerBytes []byte, ts uint64, err error) {
	return parseHeaderRecord(journalMagic, b)
}

func parseBackupRecord(b []byte) (headerBytes []byte, ts uint64, err error) {
	return parseHeaderRecord(backupMagic, b)
}

func parseHeaderRecord(magic [4]byte, b []byte) (headerBytes []byte, ts uint64, err error) {
	if len(b) != journalRecordSize {
		return nil, 0, ErrInvalidFormat
	}
	if !bytes.Equal(b[:4], magic[:]) {
		return nil, 0, ErrInvalidFormat
	}
	recordCRC := binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc32.ChecksumIEEE(b[:len(b)-4]) != recordCRC {
		return nil, 0, ErrInvalidFormat
	}
	ts = binary.LittleEndian.Uint64(b[4:12])
	headerBytes = append([]byte(nil), b[12:12+format.HeaderSize]...)
	headerCRC := binary.LittleEndian.Uint32(b[12+format.HeaderSize : 12+format.HeaderSize+4])
	if crc32.ChecksumIEEE(headerBytes) != headerCRC {
		return nil, 0, ErrInvalidFormat
	}
	return headerBytes, ts, nil
}

func recoverHeaderFromJournal(f *os.File) (format.HeaderV1, error) {
	h, _, err := recoverHeaderByMagic(f, journalMagic)
	return h, err
}

func recoverHeaderFromBackup(f *os.File) (format.HeaderV1, error) {
	h, _, err := recoverHeaderByMagic(f, backupMagic)
	return h, err
}

func recoverHeaderFromFixedBackup(f *os.File) (format.HeaderV1, error) {
	buf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(buf, fixedBackupOffset); err != nil {
		return format.HeaderV1{}, err
	}
	h, err := format.UnmarshalHeaderV1(buf)
	if err != nil {
		return format.HeaderV1{}, err
	}
	if err := validateHeaderDirectory(f, h); err != nil {
		return format.HeaderV1{}, err
	}
	return h, nil
}

func recoverBestHeader(f *os.File) (format.HeaderV1, error) {
	hj, tsj, errj := recoverHeaderByMagic(f, journalMagic)
	hb, tsb, errb := recoverHeaderByMagic(f, backupMagic)
	if errj != nil && errb != nil {
		return format.HeaderV1{}, ErrInvalidFormat
	}
	if errj == nil && errb != nil {
		return hj, nil
	}
	if errb == nil && errj != nil {
		return hb, nil
	}
	if tsb >= tsj {
		return hb, nil
	}
	return hj, nil
}

func recoverHeaderByMagic(f *os.File, magic [4]byte) (format.HeaderV1, uint64, error) {
	st, err := f.Stat()
	if err != nil {
		return format.HeaderV1{}, 0, err
	}
	size := st.Size()
	if size < int64(journalRecordSize) {
		return format.HeaderV1{}, 0, ErrInvalidFormat
	}
	window := int64(journalSearchBytes)
	if size < window {
		window = size
	}
	start := size - window
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return format.HeaderV1{}, 0, err
	}

	for i := len(buf) - journalRecordSize; i >= 0; i-- {
		if !bytes.Equal(buf[i:i+4], magic[:]) {
			continue
		}
		headerBytes, ts, err := parseHeaderRecord(magic, buf[i:i+journalRecordSize])
		if err != nil {
			continue
		}
		h, err := format.UnmarshalHeaderV1(headerBytes)
		if err != nil {
			continue
		}
		if err := validateHeaderDirectory(f, h); err != nil {
			continue
		}
		return h, ts, nil
	}
	return format.HeaderV1{}, 0, ErrInvalidFormat
}
