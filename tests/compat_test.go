package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
)

type vectorManifest struct {
	Name                 string                `json:"name"`
	Description          string                `json:"description"`
	RequiredCodecs       []string              `json:"required_codecs,omitempty"`
	ExpectVerifyAllError bool                  `json:"expect_verify_all_error"`
	Entries              []vectorManifestEntry `json:"entries"`
}

type vectorManifestEntry struct {
	Path             string `json:"path"`
	ExpectedFile     string `json:"expected_file,omitempty"`
	ExpectExtractErr string `json:"expect_extract_err,omitempty"`
}

func TestCanonicalVectors(t *testing.T) {
	root := filepath.Join("testdata", "vectors")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read vectors dir: %v", err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		vectorDir := filepath.Join(root, d.Name())
		manifestPath := filepath.Join(vectorDir, "manifest.json")
		b, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("read manifest %s: %v", manifestPath, err)
		}
		var m vectorManifest
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("parse manifest %s: %v", manifestPath, err)
		}

		t.Run(m.Name, func(t *testing.T) {
			for _, codecName := range m.RequiredCodecs {
				if !supportsCodecName(codecName) {
					t.Skipf("required codec %s is not available in this build", codecName)
				}
			}
			containerPath := filepath.Join(vectorDir, "container.fbx")
			c, err := fbx.Open(containerPath, nil)
			if err != nil {
				t.Fatalf("open container: %v", err)
			}
			defer c.Close()

			wantPaths := make([]string, 0, len(m.Entries))
			for _, e := range m.Entries {
				wantPaths = append(wantPaths, e.Path)
			}
			sort.Strings(wantPaths)

			it := c.List()
			gotPaths := make([]string, 0, len(wantPaths))
			for it.Next() {
				gotPaths = append(gotPaths, it.Value().Path)
			}
			if err := it.Err(); err != nil {
				t.Fatalf("list error: %v", err)
			}
			sort.Strings(gotPaths)
			if len(gotPaths) != len(wantPaths) {
				t.Fatalf("list length mismatch: got=%v want=%v", gotPaths, wantPaths)
			}
			for i := range wantPaths {
				if gotPaths[i] != wantPaths[i] {
					t.Fatalf("list mismatch: got=%v want=%v", gotPaths, wantPaths)
				}
			}

			report, err := c.Verify(&fbx.VerifyOptions{Mode: fbx.VerifyAllChunks})
			if m.ExpectVerifyAllError {
				if err == nil {
					t.Fatalf("expected verify-all error, report=%+v", report)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected verify-all error: %v (report=%+v)", err, report)
				}
			}

			for _, e := range m.Entries {
				var out bytes.Buffer
				err := c.Extract(e.Path, &out)
				if e.ExpectExtractErr != "" {
					if !matchesExtractErr(err, e.ExpectExtractErr) {
						t.Fatalf("extract %s: expected error %q got %v", e.Path, e.ExpectExtractErr, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("extract %s: %v", e.Path, err)
				}
				expectedPath := filepath.Join(vectorDir, filepath.FromSlash(e.ExpectedFile))
				want, err := os.ReadFile(expectedPath)
				if err != nil {
					t.Fatalf("read expected file %s: %v", expectedPath, err)
				}
				if !bytes.Equal(out.Bytes(), want) {
					t.Fatalf("extract mismatch for %s", e.Path)
				}
			}
		})
	}
}

func matchesExtractErr(err error, code string) bool {
	if code == "crc_mismatch" {
		return errors.Is(err, fbx.ErrCRCMismatch)
	}
	if code == "not_found" {
		return errors.Is(err, fbx.ErrNotFound)
	}
	if code == "invalid_format" {
		return errors.Is(err, fbx.ErrInvalidFormat)
	}
	if code == "" {
		return err == nil
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(code))
}

func supportsCodecName(codecName string) bool {
	var c format.Codec
	switch strings.ToLower(codecName) {
	case "store":
		c = format.CodecStore
	case "zstd":
		c = format.CodecZstd
	case "lz4":
		c = format.CodecLZ4
	default:
		return false
	}
	_, _, err := format.EncodeChunkRecord([]byte("codec-probe"), c, 1)
	return err == nil
}
