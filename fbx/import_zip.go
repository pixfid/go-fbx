package fbx

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/pixfid/go-fbx/internal/pathutil"
)

type ZIPImportOptions struct {
	IncludeMetadata  bool
	MetaFile         string
	PathPrefix       string
	Overwrite        bool
	ContainerOptions *Options
	WriteOptions     *WriteOptions
	Progress         func(ZIPImportProgress)
}

type ZIPImportProgress struct {
	Phase          string
	FilePath       string
	FilesDone      int
	FilesTotal     int
	FileBytesDone  uint64
	FileBytesTotal uint64
	BytesDone      uint64
	BytesTotal     uint64
}

func ConvertZIPToFBX(zipPath, fbxPath string, opts *ZIPImportOptions) error {
	cfg := ZIPImportOptions{IncludeMetadata: true}
	if opts != nil {
		cfg = *opts
		if opts.IncludeMetadata {
			cfg.IncludeMetadata = true
		}
	}
	if !cfg.Overwrite {
		if _, err := os.Stat(fbxPath); err == nil {
			return fmt.Errorf("fbx: output file already exists: %s", fbxPath)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	metaMap, err := loadMetaFile(cfg.MetaFile)
	if err != nil {
		return err
	}

	zf, err := os.Open(zipPath)
	if err != nil {
		return err
	}
	defer zf.Close()
	st, err := zf.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(zf, st.Size())
	if err != nil {
		return err
	}
	totalFiles, totalBytes := zipTotals(zr)
	emitProgress(cfg.Progress, ZIPImportProgress{
		Phase:      "start",
		FilesTotal: totalFiles,
		BytesTotal: totalBytes,
	})

	c, err := Create(fbxPath, cfg.ContainerOptions)
	if err != nil {
		return err
	}
	defer c.Close()

	tx, err := c.Begin()
	if err != nil {
		return err
	}

	filesDone := 0
	var bytesDone uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		targetPath := f.Name
		if cfg.PathPrefix != "" {
			targetPath = strings.Trim(cfg.PathPrefix, "/") + "/" + strings.TrimLeft(targetPath, "/")
		}
		norm, err := pathutil.Normalize(targetPath)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("invalid zip path %q: %w", f.Name, err)
		}
		meta, err := buildEntryMetadata(zipPath, f, norm, cfg.IncludeMetadata, metaMap)
		if err != nil {
			tx.Rollback()
			return err
		}

		emitProgress(cfg.Progress, ZIPImportProgress{
			Phase:          "file_start",
			FilePath:       norm,
			FilesDone:      filesDone,
			FilesTotal:     totalFiles,
			FileBytesTotal: f.UncompressedSize64,
			BytesDone:      bytesDone,
			BytesTotal:     totalBytes,
		})

		rc, err := f.Open()
		if err != nil {
			tx.Rollback()
			return err
		}
		fileDone := uint64(0)
		pr := &progressReadCloser{
			ReadCloser: rc,
			onRead: func(n int) {
				delta := uint64(n)
				fileDone += delta
				bytesDone += delta
				emitProgress(cfg.Progress, ZIPImportProgress{
					Phase:          "file_progress",
					FilePath:       norm,
					FilesDone:      filesDone,
					FilesTotal:     totalFiles,
					FileBytesDone:  fileDone,
					FileBytesTotal: f.UncompressedSize64,
					BytesDone:      bytesDone,
					BytesTotal:     totalBytes,
				})
			},
		}
		if err := tx.Upsert(norm, pr, meta, withWriteOptsTime(cfg.WriteOptions, uint64(f.Modified.Unix()), uint32(f.Mode()))); err != nil {
			_ = rc.Close()
			tx.Rollback()
			return err
		}
		_ = rc.Close()
		filesDone++
		emitProgress(cfg.Progress, ZIPImportProgress{
			Phase:          "file_done",
			FilePath:       norm,
			FilesDone:      filesDone,
			FilesTotal:     totalFiles,
			FileBytesDone:  fileDone,
			FileBytesTotal: f.UncompressedSize64,
			BytesDone:      bytesDone,
			BytesTotal:     totalBytes,
		})
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	emitProgress(cfg.Progress, ZIPImportProgress{
		Phase:      "done",
		FilesDone:  filesDone,
		FilesTotal: totalFiles,
		BytesDone:  bytesDone,
		BytesTotal: totalBytes,
	})
	return nil
}

type progressReadCloser struct {
	io.ReadCloser
	onRead func(n int)
}

func (p *progressReadCloser) Read(b []byte) (int, error) {
	n, err := p.ReadCloser.Read(b)
	if n > 0 && p.onRead != nil {
		p.onRead(n)
	}
	return n, err
}

func zipTotals(zr *zip.Reader) (int, uint64) {
	totalFiles := 0
	var totalBytes uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		totalFiles++
		totalBytes += f.UncompressedSize64
	}
	return totalFiles, totalBytes
}

func emitProgress(cb func(ZIPImportProgress), p ZIPImportProgress) {
	if cb != nil {
		cb(p)
	}
}

func withWriteOptsTime(base *WriteOptions, mtime uint64, mode uint32) *WriteOptions {
	if base == nil {
		return &WriteOptions{MTimeUnix: mtime, Mode: mode}
	}
	out := *base
	if out.MTimeUnix == 0 {
		out.MTimeUnix = mtime
	}
	if out.Mode == 0 {
		out.Mode = mode
	}
	return &out
}

func buildEntryMetadata(zipPath string, zf *zip.File, entryPath string, includeAuto bool, sidecar map[string]json.RawMessage) ([]byte, error) {
	var data map[string]any
	if includeAuto {
		data = map[string]any{
			"source":            zipPath,
			"zip_name":          zf.Name,
			"zip_method":        zf.Method,
			"zip_crc32":         zf.CRC32,
			"zip_modified_unix": zf.Modified.Unix(),
			"zip_compressed":    zf.CompressedSize64,
			"zip_uncompressed":  zf.UncompressedSize64,
		}
		ext := strings.ToLower(filepath.Ext(zf.Name))
		if ext != "" {
			if typ := mime.TypeByExtension(ext); typ != "" {
				data["mime"] = typ
			}
		}
	}

	raw, ok := sidecar[entryPath]
	if !ok {
		raw = sidecar[zf.Name]
	}
	if len(raw) > 0 {
		if data == nil {
			return append([]byte(nil), raw...), nil
		}
		var overlay map[string]any
		if err := json.Unmarshal(raw, &overlay); err != nil {
			return nil, fmt.Errorf("fbx: meta-file value for %q is not object: %w", entryPath, err)
		}
		for k, v := range overlay {
			data[k] = v
		}
	}
	if len(data) == 0 {
		return nil, nil
	}
	return json.Marshal(data)
}

func loadMetaFile(path string) (map[string]json.RawMessage, error) {
	if path == "" {
		return map[string]json.RawMessage{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("fbx: invalid meta file %s: %w", path, err)
	}
	return m, nil
}
