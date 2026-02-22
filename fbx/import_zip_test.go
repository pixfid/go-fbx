package fbx

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestZipTotalsAndProgressReadCloser(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "a.zip")
	if err := writeTestZIP(zp, map[string]string{"a.txt": "123", "b/c.txt": "45"}); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	f, err := os.Open(zp)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer f.Close()
	st, _ := f.Stat()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	files, bytesTotal := zipTotals(zr)
	if files != 2 || bytesTotal != 5 {
		t.Fatalf("unexpected totals files=%d bytes=%d", files, bytesTotal)
	}

	rc := io.NopCloser(bytes.NewReader([]byte("abcde")))
	reads := 0
	pr := &progressReadCloser{ReadCloser: rc, onRead: func(n int) { reads += n }}
	_, _ = io.Copy(io.Discard, pr)
	if reads != 5 {
		t.Fatalf("unexpected read counter: %d", reads)
	}

	called := false
	emitProgress(func(ZIPImportProgress) { called = true }, ZIPImportProgress{Phase: "done"})
	if !called {
		t.Fatalf("expected callback")
	}
	emitProgress(nil, ZIPImportProgress{})
}

func TestWithWriteOptsTime(t *testing.T) {
	w := withWriteOptsTime(nil, 11, 22)
	if w.MTimeUnix != 11 || w.Mode != 22 {
		t.Fatalf("unexpected opts: %+v", w)
	}
	base := &WriteOptions{Codec: CodecStore, MTimeUnix: 1, Mode: 2}
	w = withWriteOptsTime(base, 11, 22)
	if w.MTimeUnix != 1 || w.Mode != 2 {
		t.Fatalf("explicit fields should be kept: %+v", w)
	}
}

func TestBuildEntryMetadataAndLoadMetaFile(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "m.zip")
	if err := writeTestZIP(zp, map[string]string{"book.fb2": "<fb2/>"}); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	f, err := os.Open(zp)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer f.Close()
	st, _ := f.Stat()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}

	sidecarPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(sidecarPath, []byte(`{"book.fb2":{"tag":"ok"}}`), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	metaMap, err := loadMetaFile(sidecarPath)
	if err != nil {
		t.Fatalf("load sidecar: %v", err)
	}
	meta, err := buildEntryMetadata("src.zip", zr.File[0], "book.fb2", true, metaMap)
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if m["zip_name"] != "book.fb2" || m["tag"] != "ok" {
		t.Fatalf("unexpected merged metadata: %+v", m)
	}

	if _, err := loadMetaFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatalf("expected missing file error")
	}
	badPath := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(badPath, []byte("{"), 0o644)
	if _, err := loadMetaFile(badPath); err == nil {
		t.Fatalf("expected invalid sidecar json error")
	}
}

func TestConvertZIPToFBXWithLimits(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "in.zip")
	out := filepath.Join(dir, "out.fbx")
	if err := writeTestZIP(zp, map[string]string{"a.txt": "hello"}); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	err := ConvertZIPToFBX(zp, out, &ZIPImportOptions{
		ContainerOptions: &Options{MaxEntrySize: 4, DetectText: true, StoreIfAlreadyCompressed: true, SyncOnCommit: true, StrictVerify: true},
	})
	if err == nil {
		t.Fatalf("expected limit error")
	}

	err = ConvertZIPToFBX(zp, out, &ZIPImportOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	c, err := Open(out, nil)
	if err != nil {
		t.Fatalf("open out: %v", err)
	}
	defer c.Close()
	var b bytes.Buffer
	if err := c.Extract("a.txt", &b); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if b.String() != "hello" {
		t.Fatalf("unexpected body: %q", b.String())
	}
}

func writeTestZIP(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return nil
}
