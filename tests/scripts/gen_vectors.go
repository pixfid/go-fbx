//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
)

type Manifest struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	RequiredCodecs       []string        `json:"required_codecs,omitempty"`
	ExpectVerifyAllError bool            `json:"expect_verify_all_error"`
	Entries              []ManifestEntry `json:"entries"`
}

type ManifestEntry struct {
	Path             string `json:"path"`
	ExpectedFile     string `json:"expected_file,omitempty"`
	ExpectExtractErr string `json:"expect_extract_err,omitempty"`
}

func main() {
	root := filepath.Clean("tests/testdata/vectors")
	must(os.MkdirAll(root, 0o755))

	must(genV1(filepath.Join(root, "v1-min-store")))
	must(genV3(filepath.Join(root, "v3-replace")))
	must(genV4(filepath.Join(root, "v4-remove")))
	must(genV5(filepath.Join(root, "v5-corrupt-crc")))
}

func genV1(dir string) error {
	if err := resetDir(dir); err != nil {
		return err
	}
	container := filepath.Join(dir, "container.fbx")
	c, err := fbx.Create(container, nil)
	if err != nil {
		return err
	}
	body := []byte("<FictionBook>v1</FictionBook>\n")
	if err := c.Add("book.fb2", bytes.NewReader(body), nil, &fbx.WriteOptions{Codec: fbx.CodecStore}); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.Close(); err != nil {
		return err
	}
	if err := writeExpected(dir, "book.fb2", body); err != nil {
		return err
	}
	return writeManifest(dir, Manifest{
		Name:        "v1-min-store",
		Description: "Minimal container with one STORE chunk",
		Entries: []ManifestEntry{{
			Path:         "book.fb2",
			ExpectedFile: "expected/book.fb2",
		}},
	})
}

func genV3(dir string) error {
	if err := resetDir(dir); err != nil {
		return err
	}
	container := filepath.Join(dir, "container.fbx")
	c, err := fbx.Create(container, nil)
	if err != nil {
		return err
	}
	if err := c.Add("book.fb2", bytes.NewReader([]byte("old-version\n")), nil, nil); err != nil {
		_ = c.Close()
		return err
	}
	newBody := []byte("new-version\n")
	if err := c.Replace("book.fb2", bytes.NewReader(newBody), nil, nil); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.Close(); err != nil {
		return err
	}
	if err := writeExpected(dir, "book.fb2", newBody); err != nil {
		return err
	}
	return writeManifest(dir, Manifest{
		Name:        "v3-replace",
		Description: "Entry replaced; latest directory points to new content",
		Entries: []ManifestEntry{{
			Path:         "book.fb2",
			ExpectedFile: "expected/book.fb2",
		}},
	})
}

func genV4(dir string) error {
	if err := resetDir(dir); err != nil {
		return err
	}
	container := filepath.Join(dir, "container.fbx")
	c, err := fbx.Create(container, nil)
	if err != nil {
		return err
	}
	if err := c.Add("a.fb2", bytes.NewReader([]byte("a\n")), nil, nil); err != nil {
		_ = c.Close()
		return err
	}
	b := []byte("b\n")
	if err := c.Add("b.fb2", bytes.NewReader(b), nil, nil); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.Remove("a.fb2"); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.Close(); err != nil {
		return err
	}
	if err := writeExpected(dir, "b.fb2", b); err != nil {
		return err
	}
	return writeManifest(dir, Manifest{
		Name:        "v4-remove",
		Description: "Entry removed from latest directory",
		Entries: []ManifestEntry{{
			Path:         "b.fb2",
			ExpectedFile: "expected/b.fb2",
		}},
	})
}

func genV5(dir string) error {
	if err := resetDir(dir); err != nil {
		return err
	}
	container := filepath.Join(dir, "container.fbx")
	c, err := fbx.Create(container, nil)
	if err != nil {
		return err
	}
	orig := []byte("corrupt-me\n")
	if err := c.Add("book.fb2", bytes.NewReader(orig), nil, &fbx.WriteOptions{Codec: fbx.CodecStore}); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.Close(); err != nil {
		return err
	}
	if err := corruptFirstPayloadByte(container, "book.fb2"); err != nil {
		return err
	}
	return writeManifest(dir, Manifest{
		Name:                 "v5-corrupt-crc",
		Description:          "Chunk payload is corrupted; extraction must fail with crc_mismatch",
		ExpectVerifyAllError: true,
		Entries: []ManifestEntry{{
			Path:             "book.fb2",
			ExpectExtractErr: "crc_mismatch",
		}},
	})
}

func corruptFirstPayloadByte(containerPath, entryPath string) error {
	entry, err := readEntry(containerPath, entryPath)
	if err != nil {
		return err
	}
	if len(entry.Chunks) == 0 {
		return errors.New("no chunks")
	}
	f, err := os.OpenFile(containerPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	pos := int64(entry.Chunks[0].ChunkOffset) + 16
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, pos); err != nil {
		return err
	}
	b[0] ^= 0x01
	if _, err := f.WriteAt(b, pos); err != nil {
		return err
	}
	return f.Sync()
}

func readEntry(containerPath, entryPath string) (format.EntryV1, error) {
	f, err := os.Open(containerPath)
	if err != nil {
		return format.EntryV1{}, err
	}
	defer f.Close()
	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		return format.EntryV1{}, err
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		return format.EntryV1{}, err
	}
	blob := make([]byte, h.DirSize)
	if _, err := f.ReadAt(blob, int64(h.DirOffset)); err != nil {
		return format.EntryV1{}, err
	}
	d, err := format.DecodeDirectory(blob, h.DirCRC32, h.DirSize)
	if err != nil {
		return format.EntryV1{}, err
	}
	for _, e := range d.Entries {
		if e.Path == entryPath {
			return e, nil
		}
	}
	return format.EntryV1{}, fmt.Errorf("entry not found: %s", entryPath)
}

func writeManifest(dir string, m Manifest) error {
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o644)
}

func writeExpected(dir, rel string, data []byte) error {
	p := filepath.Join(dir, "expected", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func resetDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
