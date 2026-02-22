package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go-fbx/fbx"
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

func TestRunReplaceAndSetMetaAndStat(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "c.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader([]byte("old")), []byte(`{"v":1}`), nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	src := filepath.Join(dir, "new.fb2")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if code := runReplace([]string{"--as", "books/a.fb2", containerPath, src}); code != 0 {
		t.Fatalf("runReplace exit code: %d", code)
	}

	c2, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open after replace: %v", err)
	}
	defer c2.Close()
	var out bytes.Buffer
	if err := c2.Extract("books/a.fb2", &out); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if out.String() != "new" {
		t.Fatalf("unexpected body after replace: %q", out.String())
	}
	info, err := c2.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if string(info.Meta) != `{"v":1}` {
		t.Fatalf("meta should be preserved by default, got: %s", string(info.Meta))
	}
	_ = c2.Close()

	if code := runSetMeta([]string{"--meta-json", `{"v":2}`, containerPath, "books/a.fb2"}); code != 0 {
		t.Fatalf("runSetMeta exit code: %d", code)
	}
	if code := runStat([]string{"--json", containerPath, "books/a.fb2"}); code != 0 {
		t.Fatalf("runStat exit code: %d", code)
	}
	c3, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open after set-meta: %v", err)
	}
	defer c3.Close()
	info, err = c3.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("stat after set-meta: %v", err)
	}
	if string(info.Meta) != `{"v":2}` {
		t.Fatalf("unexpected meta after set-meta: %s", string(info.Meta))
	}
}

func TestRunRmWithWhereFilters(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "c.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/small.fb2", bytes.NewReader([]byte("1234")), nil, nil); err != nil {
		t.Fatalf("add small: %v", err)
	}
	if err := c.Add("books/big.fb2", bytes.NewReader([]byte("123456789")), nil, nil); err != nil {
		t.Fatalf("add big: %v", err)
	}
	if err := c.Add("other/keep.fb2", bytes.NewReader([]byte("123456789")), nil, nil); err != nil {
		t.Fatalf("add keep: %v", err)
	}
	_ = c.Close()

	if code := runRm([]string{"--contains", "books/", "--min-size", "5", containerPath}); code != 0 {
		t.Fatalf("runRm exit code: %d", code)
	}
	c2, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c2.Close()
	if _, err := c2.Stat("books/big.fb2"); err == nil {
		t.Fatalf("books/big.fb2 must be removed")
	}
	if _, err := c2.Stat("books/small.fb2"); err != nil {
		t.Fatalf("books/small.fb2 must remain: %v", err)
	}
	if _, err := c2.Stat("other/keep.fb2"); err != nil {
		t.Fatalf("other/keep.fb2 must remain: %v", err)
	}
}
