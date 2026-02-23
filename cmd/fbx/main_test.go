package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pixfid/go-fbx/fbx"
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

func TestInteractiveSessionBasicFlow(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "c.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("books/a.fb2", bytes.NewReader([]byte("0123456789")), []byte(`{"id":1}`), nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := c.Add("books/b.fb2", bytes.NewReader([]byte("content-b")), nil, nil); err != nil {
		t.Fatalf("add b: %v", err)
	}
	_ = c.Close()

	var out bytes.Buffer
	var errOut bytes.Buffer
	s := &interactiveSession{
		opts:         nil,
		out:          &out,
		errOut:       &errOut,
		lastViewSize: 4,
	}
	defer s.closeContainer()

	if exit, code := s.runCommand("open " + containerPath); exit || code != 0 {
		t.Fatalf("open failed: exit=%v code=%d stderr=%s", exit, code, errOut.String())
	}
	if _, code := s.runCommand("ls"); code != 0 {
		t.Fatalf("ls failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "books/") {
		t.Fatalf("expected books/ in ls output, got: %s", out.String())
	}
	out.Reset()
	if _, code := s.runCommand("cd books"); code != 0 {
		t.Fatalf("cd failed: %s", errOut.String())
	}
	if _, code := s.runCommand("pwd"); code != 0 {
		t.Fatalf("pwd failed: %s", errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "/books" {
		t.Fatalf("unexpected pwd: %q", got)
	}
	out.Reset()
	if _, code := s.runCommand("stat a.fb2"); code != 0 {
		t.Fatalf("stat failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "path=books/a.fb2") {
		t.Fatalf("unexpected stat output: %s", out.String())
	}
	out.Reset()
	if _, code := s.runCommand("cat a.fb2 0 4"); code != 0 {
		t.Fatalf("cat failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "0123") {
		t.Fatalf("unexpected cat output: %s", out.String())
	}
	out.Reset()
	if _, code := s.runCommand("next"); code != 0 {
		t.Fatalf("next failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "4567") {
		t.Fatalf("unexpected next output: %s", out.String())
	}
	out.Reset()
	if _, code := s.runCommand("rm a.fb2"); code != 0 {
		t.Fatalf("rm failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "removed books/a.fb2") {
		t.Fatalf("unexpected rm output: %s", out.String())
	}
}

func TestInteractiveSessionPrevAndErrors(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "c.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.txt", bytes.NewReader([]byte("abcdefghij")), nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	var out bytes.Buffer
	var errOut bytes.Buffer
	s := &interactiveSession{out: &out, errOut: &errOut, lastViewSize: 4}
	defer s.closeContainer()

	if _, code := s.runCommand("next"); code == 0 {
		t.Fatalf("next should fail when nothing viewed")
	}
	if _, code := s.runCommand("open " + containerPath); code != 0 {
		t.Fatalf("open failed: %s", errOut.String())
	}
	if _, code := s.runCommand("cat a.txt 4 4"); code != 0 {
		t.Fatalf("cat failed: %s", errOut.String())
	}
	out.Reset()
	if _, code := s.runCommand("prev"); code != 0 {
		t.Fatalf("prev failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "abcd") {
		t.Fatalf("unexpected prev output: %s", out.String())
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

func TestBrowserModelNavigationAndDelete(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "ui.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("books/%d.fb2", i)
		if err := c.Add(name, bytes.NewReader([]byte("payload")), []byte(`{"i":1}`), nil); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	_ = c.Close()

	s := &interactiveSession{opts: nil}
	if err := s.openContainer(containerPath); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.closeContainer()
	m := newBrowserModel(s)
	m.reload()
	if len(m.entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(m.entries))
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(browserModel)
	if m.selected != 1 {
		t.Fatalf("expected selected=1, got %d", m.selected)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(browserModel)
	if m.focusPane != 1 {
		t.Fatalf("expected focusPane=1, got %d", m.focusPane)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(browserModel)
	if !m.confirmDelete {
		t.Fatalf("delete confirm must be active")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(browserModel)
	if len(m.entries) != 2 {
		t.Fatalf("expected one deleted entry, got %d", len(m.entries))
	}
}

func TestBrowserModelRendersPseudoGraphics(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "ui2.fbx")
	c, err := fbx.Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("177692.fb2", bytes.NewReader([]byte("hello world")), []byte(`{"meta":"x"}`), nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	_ = c.Close()

	s := &interactiveSession{}
	if err := s.openContainer(containerPath); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.closeContainer()
	m := newBrowserModel(s)
	m.width = 100
	m.height = 24
	m.reload()
	v := m.View()
	if !strings.Contains(v, "─") || !strings.Contains(v, "│") {
		t.Fatalf("expected pseudographics in view:\n%s", v)
	}
	if !strings.Contains(v, "характеристики записи") {
		t.Fatalf("expected record characteristics section:\n%s", v)
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

func TestInteractiveModelAcceptsSpace(t *testing.T) {
	m := newInteractiveModel(&interactiveSession{})
	var model tea.Model = m
	for _, r := range []rune("cat") {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	got := model.(interactiveModel).input
	if got != "cat 1" {
		t.Fatalf("unexpected input: %q", got)
	}
}
