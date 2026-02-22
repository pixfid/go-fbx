package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCodec(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{in: "store"},
		{in: "zstd"},
		{in: "lz4"},
		{in: "bad", wantErr: true},
	}
	for _, tc := range tests {
		_, err := parseCodec(tc.in)
		if tc.wantErr && err == nil {
			t.Fatalf("parseCodec(%q): expected error", tc.in)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("parseCodec(%q): unexpected error: %v", tc.in, err)
		}
	}
}

func TestBuildLimitOptions(t *testing.T) {
	opts, err := buildLimitOptions(0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts != nil {
		t.Fatalf("expected nil options when limits are zero")
	}

	opts, err = buildLimitOptions(1024, 2048)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts == nil || opts.MaxEntrySize != 1024 || opts.MaxChunkSize != 2048 {
		t.Fatalf("unexpected options: %+v", opts)
	}

	_, err = buildLimitOptions(0, uint64(^uint32(0))+1)
	if err == nil {
		t.Fatalf("expected error for too large chunk limit")
	}
}

func TestLoadMeta(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("write meta file: %v", err)
	}

	if _, err := loadMeta(`{"ok":true}`, metaPath); err == nil {
		t.Fatalf("expected error when both meta-json and meta-file are set")
	}
	if _, err := loadMeta(`{`, ""); err == nil {
		t.Fatalf("expected invalid json error")
	}
	b, err := loadMeta("", metaPath)
	if err != nil {
		t.Fatalf("loadMeta file: %v", err)
	}
	if string(b) != `{"a":1}` {
		t.Fatalf("unexpected meta bytes: %s", string(b))
	}
}

func TestZipProgressPrinterRender(t *testing.T) {
	p := &zipProgressPrinter{}
	p.render(3, 10, false)
	if p.lastLine == "" {
		t.Fatalf("expected rendered line")
	}
	before := p.lastLine
	p.render(3, 10, false)
	if p.lastLine != before {
		t.Fatalf("last line changed unexpectedly")
	}
	p.render(10, 10, true)
	if p.lastLine == "" {
		t.Fatalf("expected final rendered line")
	}
}
