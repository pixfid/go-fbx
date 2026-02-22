package fbx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/internal/format"
)

func TestJournalRecordRoundTrip(t *testing.T) {
	h := format.HeaderV1{Magic: format.MagicHeader, Version: format.VersionV1, HeaderSize: format.HeaderSize, DirOffset: 128, DirSize: 64, DirCRC32: 1}
	hb, err := h.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	rec, err := buildJournalRecord(hb, 10)
	if err != nil {
		t.Fatalf("build journal: %v", err)
	}
	out, ts, err := parseJournalRecord(rec)
	if err != nil {
		t.Fatalf("parse journal: %v", err)
	}
	if ts != 10 || !bytes.Equal(out, hb) {
		t.Fatalf("unexpected parsed record")
	}

	rec[len(rec)-1] ^= 0x01
	if _, _, err := parseJournalRecord(rec); err == nil {
		t.Fatalf("expected record CRC failure")
	}
}

func TestRecoveryHelpers(t *testing.T) {
	path := createContainerForRecovery(t)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	hb := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(hb, 0); err != nil {
		t.Fatalf("read header: %v", err)
	}

	jr, err := buildJournalRecord(hb, 5)
	if err != nil {
		t.Fatalf("build journal: %v", err)
	}
	br, err := buildBackupRecord(hb, 8)
	if err != nil {
		t.Fatalf("build backup: %v", err)
	}
	st, _ := f.Stat()
	if _, err := f.WriteAt(jr, st.Size()); err != nil {
		t.Fatalf("append journal: %v", err)
	}
	if _, err := f.WriteAt(br, st.Size()+int64(len(jr))); err != nil {
		t.Fatalf("append backup: %v", err)
	}

	if _, err := recoverHeaderFromJournal(f); err != nil {
		t.Fatalf("recover journal: %v", err)
	}
	if _, err := recoverHeaderFromBackup(f); err != nil {
		t.Fatalf("recover backup: %v", err)
	}
	if _, err := recoverHeaderFromFixedBackup(f); err != nil {
		t.Fatalf("recover fixed backup: %v", err)
	}
	if _, err := recoverBestHeader(f); err != nil {
		t.Fatalf("recover best: %v", err)
	}
}

func createContainerForRecovery(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "recover.fbx")
	c, err := Create(path, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.txt", bytes.NewReader([]byte("hello")), nil, nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}
