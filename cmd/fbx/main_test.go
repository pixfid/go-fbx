package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
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

func TestInspectContainerCodecsRejectsOversizedDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized-inspect.fbx")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	h := format.HeaderV1{
		Magic:      format.MagicHeader,
		Version:    format.VersionV1,
		HeaderSize: format.HeaderSize,
		DirOffset:  format.HeaderSize,
		DirSize:    maxInspectDirectoryBlobSize + 1,
	}
	hb, err := h.MarshalBinary()
	if err != nil {
		_ = f.Close()
		t.Fatalf("marshal header: %v", err)
	}
	if _, err := f.WriteAt(hb, 0); err != nil {
		_ = f.Close()
		t.Fatalf("write header: %v", err)
	}
	if err := f.Truncate(int64(h.DirOffset + h.DirSize)); err != nil {
		_ = f.Close()
		t.Fatalf("truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = inspectContainerCodecs(path)
	if !errors.Is(err, fbx.ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
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

func TestLoadMetaMap(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "meta-map.json")
	if err := os.WriteFile(okPath, []byte(`{"books/a.fb2":{"id":1},"books/b.fb2":[1,2,3]}`), 0o644); err != nil {
		t.Fatalf("write meta map: %v", err)
	}
	m, err := loadMetaMap(okPath)
	if err != nil {
		t.Fatalf("loadMetaMap: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("unexpected size: %d", len(m))
	}

	badPath := filepath.Join(dir, "meta-map-bad.json")
	if err := os.WriteFile(badPath, []byte(`[]`), 0o644); err != nil {
		t.Fatalf("write bad map: %v", err)
	}
	if _, err := loadMetaMap(badPath); err == nil {
		t.Fatalf("expected error for non-object meta map")
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

func TestRunSetMetaMany(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "meta-many.fbx")
	bodyA := bytes.Repeat([]byte("a-"), 64)
	bodyB := bytes.Repeat([]byte("b-"), 64)

	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader(bodyA), []byte(`{"v":1}`), &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		_ = c.Close()
		t.Fatalf("add a: %v", err)
	}
	if err := c.Add("books/b.fb2", bytes.NewReader(bodyB), []byte(`{"v":1}`), &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		_ = c.Close()
		t.Fatalf("add b: %v", err)
	}
	_ = c.Close()

	metaMapPath := filepath.Join(dir, "meta-map.json")
	if err := os.WriteFile(metaMapPath, []byte(`{"books/a.fb2":{"v":2},"books/b.fb2":{"v":3}}`), 0o644); err != nil {
		t.Fatalf("write meta map: %v", err)
	}
	out := captureStdout(t, func() {
		if code := runSetMetaMany([]string{"--meta-file", metaMapPath, containerPath}); code != 0 {
			t.Fatalf("runSetMetaMany exit code: %d", code)
		}
	})
	if !strings.Contains(out, "entries_updated=2") {
		t.Fatalf("expected updated output, got: %s", out)
	}
	rep, err := inspectContainerCodecs(containerPath)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if rep.DeadBytes != 0 || rep.ChurnOps != 0 {
		t.Fatalf("metadata-only update must not create dead bytes/churn, got dead=%d churn=%d", rep.DeadBytes, rep.ChurnOps)
	}

	c, err = fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()
	infoA, err := c.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("stat a: %v", err)
	}
	infoB, err := c.Stat("books/b.fb2")
	if err != nil {
		t.Fatalf("stat b: %v", err)
	}
	if string(infoA.Meta) != `{"v":2}` || string(infoB.Meta) != `{"v":3}` {
		t.Fatalf("unexpected metadata after set-meta-many: a=%s b=%s", string(infoA.Meta), string(infoB.Meta))
	}
	var gotA, gotB bytes.Buffer
	if err := c.Extract("books/a.fb2", &gotA); err != nil {
		t.Fatalf("extract a: %v", err)
	}
	if err := c.Extract("books/b.fb2", &gotB); err != nil {
		t.Fatalf("extract b: %v", err)
	}
	if !bytes.Equal(gotA.Bytes(), bodyA) || !bytes.Equal(gotB.Bytes(), bodyB) {
		t.Fatalf("payload changed after metadata-only update")
	}
}

func TestRunSetMetaManyIgnoreMissing(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "meta-many-missing.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader([]byte("a")), []byte(`{"v":1}`), nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	metaMapPath := filepath.Join(dir, "meta-map.json")
	if err := os.WriteFile(metaMapPath, []byte(`{"books/a.fb2":{"v":2},"books/missing.fb2":{"v":0}}`), 0o644); err != nil {
		t.Fatalf("write meta map: %v", err)
	}
	out := captureStdout(t, func() {
		if code := runSetMetaMany([]string{"--meta-file", metaMapPath, "--ignore-missing", containerPath}); code != 0 {
			t.Fatalf("runSetMetaMany exit code: %d", code)
		}
	})
	if !strings.Contains(out, "entries_updated=1") || !strings.Contains(out, "missing=1") {
		t.Fatalf("unexpected output: %s", out)
	}

	c, err = fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()
	info, err := c.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if string(info.Meta) != `{"v":2}` {
		t.Fatalf("unexpected metadata: %s", string(info.Meta))
	}
}

func TestRunSetMetaManyMissingFailsWithoutIgnore(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "meta-many-missing-fail.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader([]byte("a")), []byte(`{"v":1}`), nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	metaMapPath := filepath.Join(dir, "meta-map.json")
	if err := os.WriteFile(metaMapPath, []byte(`{"books/a.fb2":{"v":2},"books/missing.fb2":{"v":0}}`), 0o644); err != nil {
		t.Fatalf("write meta map: %v", err)
	}
	errOut := captureStderr(t, func() {
		if code := runSetMetaMany([]string{"--meta-file", metaMapPath, containerPath}); code != 1 {
			t.Fatalf("runSetMetaMany exit code: %d", code)
		}
	})
	if errOut == "" {
		t.Fatalf("expected error output")
	}

	c, err = fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()
	info, err := c.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if string(info.Meta) != `{"v":1}` {
		t.Fatalf("metadata must remain unchanged on failed batch, got: %s", string(info.Meta))
	}
}

func TestRunUpsertKeepsMetaByDefault(t *testing.T) {
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
	if code := runUpsert([]string{"--as", "books/a.fb2", containerPath, src}); code != 0 {
		t.Fatalf("runUpsert exit code: %d", code)
	}

	c2, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open after upsert: %v", err)
	}
	defer c2.Close()
	info, err := c2.Stat("books/a.fb2")
	if err != nil {
		t.Fatalf("stat after upsert: %v", err)
	}
	if string(info.Meta) != `{"v":1}` {
		t.Fatalf("meta should be preserved by default, got: %s", string(info.Meta))
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

func TestRunInfoAndInspectCodecs(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "codecs.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.txt", bytes.NewReader([]byte("aaaa")), nil, &fbx.WriteOptions{Codec: fbx.CodecStore}); err != nil {
		t.Fatalf("add store: %v", err)
	}
	if err := c.Add("b.txt", bytes.NewReader(bytes.Repeat([]byte("bbbb"), 64)), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		t.Fatalf("add zstd: %v", err)
	}
	_ = c.Close()

	report, err := inspectContainerCodecs(containerPath)
	if err != nil {
		t.Fatalf("inspectContainerCodecs: %v", err)
	}
	if report.EntriesTotal != 2 {
		t.Fatalf("expected 2 entries, got %d", report.EntriesTotal)
	}
	if report.ChunksTotal == 0 {
		t.Fatalf("expected >0 chunks")
	}
	if !strings.Contains(report.Codec, "mixed") {
		t.Fatalf("expected mixed codec summary, got %q", report.Codec)
	}
	if report.ChunkCounts["store"] == 0 || report.ChunkCounts["zstd"] == 0 {
		t.Fatalf("expected both store and zstd counts, got %+v", report.ChunkCounts)
	}
	if !strings.Contains(report.Level, "mixed") {
		t.Fatalf("expected mixed level summary, got %q", report.Level)
	}
	if report.LevelCounts["0"] == 0 || report.LevelCounts["3"] == 0 {
		t.Fatalf("expected both level=0 and level=3 counts, got %+v", report.LevelCounts)
	}

	outText := captureStdout(t, func() {
		if code := runInfo([]string{containerPath}); code != 0 {
			t.Fatalf("runInfo exit code: %d", code)
		}
	})
	if !strings.Contains(outText, "level=") {
		t.Fatalf("plain output must contain level, got: %s", outText)
	}

	out := captureStdout(t, func() {
		if code := runInfo([]string{"--json", containerPath}); code != 0 {
			t.Fatalf("runInfo exit code: %d", code)
		}
	})
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("runInfo json output is invalid: %v; out=%s", err, out)
	}
	if obj["codec"] == "" {
		t.Fatalf("json output must contain codec, got: %+v", obj)
	}
	if obj["level"] == "" {
		t.Fatalf("json output must contain level, got: %+v", obj)
	}
	if _, ok := obj["dead_bytes"]; !ok {
		t.Fatalf("json output must contain dead_bytes, got: %+v", obj)
	}
	if _, ok := obj["churn_ops"]; !ok {
		t.Fatalf("json output must contain churn_ops, got: %+v", obj)
	}
	if _, ok := obj["file_size"]; !ok {
		t.Fatalf("json output must contain file_size, got: %+v", obj)
	}
}

func TestRunPackMany(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "a.fbx"),
		filepath.Join(dir, "b.fbx"),
	}
	for _, p := range paths {
		c, err := fbx.Create(p, nil)
		if err != nil {
			t.Fatalf("create %s: %v", p, err)
		}
		if err := c.Add("book.fb2", bytes.NewReader(bytes.Repeat([]byte("text-"), 128)), nil, &fbx.WriteOptions{Codec: fbx.CodecStore}); err != nil {
			_ = c.Close()
			t.Fatalf("add %s: %v", p, err)
		}
		_ = c.Close()
	}

	if code := runPackMany([]string{"--jobs", "2", "--codec", "zstd", "--level", "3", paths[0], paths[1]}); code != 0 {
		t.Fatalf("runPackMany exit code: %d", code)
	}
	for _, p := range paths {
		rep, err := inspectContainerCodecs(p)
		if err != nil {
			t.Fatalf("inspect %s: %v", p, err)
		}
		if rep.ChunkCounts["zstd"] == 0 {
			t.Fatalf("expected zstd chunks in %s, got %+v", p, rep.ChunkCounts)
		}
	}
}

func TestRunPackSkipsIfAlreadyPacked(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "skip.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader(bytes.Repeat([]byte("text-"), 256)), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	errOut := captureStderr(t, func() {
		if code := runPack([]string{"--codec", "zstd", "--level", "3", "--progress=false", containerPath}); code != 0 {
			t.Fatalf("runPack exit code: %d", code)
		}
	})
	if !strings.Contains(errOut, "skip") {
		t.Fatalf("expected skip message, got: %s", errOut)
	}
}

func TestRunPackManySkipsIfAlreadyPacked(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "skip-many.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader(bytes.Repeat([]byte("text-"), 256)), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	out := captureStdout(t, func() {
		if code := runPackMany([]string{"--jobs", "1", "--codec", "zstd", "--level", "3", containerPath}); code != 0 {
			t.Fatalf("runPackMany exit code: %d", code)
		}
	})
	if !strings.Contains(out, "SKIP") {
		t.Fatalf("expected SKIP output, got: %s", out)
	}
}

func TestRunPackClearMeta(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "clear-meta.fbx")
	body := bytes.Repeat([]byte("text-"), 128)
	meta := []byte(`{"tag":"keep?"}`)

	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader(body), meta, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	errOut := captureStderr(t, func() {
		if code := runPack([]string{"--codec", "zstd", "--level", "3", "--clear-meta", "--progress=false", containerPath}); code != 0 {
			t.Fatalf("runPack exit code: %d", code)
		}
	})
	if strings.Contains(strings.ToLower(errOut), "skip") {
		t.Fatalf("expected no skip when clear-meta is set, got: %s", errOut)
	}

	co, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open packed: %v", err)
	}
	defer co.Close()
	info, err := co.Stat("book.fb2")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if len(info.Meta) != 0 {
		t.Fatalf("expected metadata to be cleared, got %q", string(info.Meta))
	}
}

func TestRunPackManyClearMeta(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "clear-meta-many.fbx")
	body := bytes.Repeat([]byte("text-"), 128)
	meta := []byte(`{"tag":"batch"}`)

	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader(body), meta, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	out := captureStdout(t, func() {
		if code := runPackMany([]string{"--jobs", "1", "--codec", "zstd", "--level", "3", "--clear-meta", containerPath}); code != 0 {
			t.Fatalf("runPackMany exit code: %d", code)
		}
	})
	if strings.Contains(out, "SKIP") {
		t.Fatalf("expected no SKIP when clear-meta is set, got: %s", out)
	}
	if !strings.Contains(out, "OK") {
		t.Fatalf("expected OK output, got: %s", out)
	}

	co, err := fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open packed: %v", err)
	}
	defer co.Close()
	info, err := co.Stat("book.fb2")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if len(info.Meta) != 0 {
		t.Fatalf("expected metadata to be cleared, got %q", string(info.Meta))
	}
}

func TestRunPackDoesNotSkipWhenDeadDataPresent(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "dead-data.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("book.fb2", bytes.NewReader(bytes.Repeat([]byte("text-"), 256)), nil, &fbx.WriteOptions{Codec: fbx.CodecStore, Level: 0}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	c, err = fbx.Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.Remove("book.fb2"); err != nil {
		_ = c.Close()
		t.Fatalf("remove: %v", err)
	}
	_ = c.Close()

	errOut := captureStderr(t, func() {
		if code := runPack([]string{"--codec", "store", "--level", "0", "--progress=false", containerPath}); code != 0 {
			t.Fatalf("runPack exit code: %d", code)
		}
	})
	if strings.Contains(strings.ToLower(errOut), "skip") {
		t.Fatalf("expected no skip when dead data is present, got: %s", errOut)
	}

	errOut = captureStderr(t, func() {
		if code := runPack([]string{"--codec", "store", "--level", "0", "--progress=false", containerPath}); code != 0 {
			t.Fatalf("runPack second exit code: %d", code)
		}
	})
	if !strings.Contains(strings.ToLower(errOut), "skip") {
		t.Fatalf("expected skip after dead data is compacted, got: %s", errOut)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured output: %v", err)
	}
	return string(b)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured error output: %v", err)
	}
	return string(b)
}
