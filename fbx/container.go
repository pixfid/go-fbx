package fbx

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/pixfid/go-fbx/internal/format"
	"github.com/pixfid/go-fbx/internal/pathutil"
)

type Container struct {
	file    *os.File
	opts    Options
	txMu    sync.Mutex
	mu      sync.RWMutex
	header  format.HeaderV1
	entries map[string]format.EntryV1
}

const maxDirectoryBlobSize uint64 = 1 << 30 // 1 GiB hard cap for directory blob reads.

func Open(path string, opts *Options) (*Container, error) {
	o := defaultOptions()
	if opts != nil {
		o = mergeOptions(o, *opts)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		_ = f.Close()
		return nil, mapErr(err)
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err == nil {
		entries, err := readEntriesByHeader(f, h)
		if err == nil {
			return &Container{file: f, opts: o, header: h, entries: entries}, nil
		}
	}

	recovered, recErr := recoverHeaderFromFixedBackup(f)
	if recErr == nil {
		entries, rerr := readEntriesByHeader(f, recovered)
		if rerr == nil {
			recoveredBuf, mErr := recovered.MarshalBinary()
			if mErr == nil {
				_, _ = f.WriteAt(recoveredBuf, 0)
				_ = f.Sync()
			}
			return &Container{file: f, opts: o, header: recovered, entries: entries}, nil
		}
	}

	recovered, recErr = recoverBestHeader(f)
	if recErr == nil {
		entries, err := readEntriesByHeader(f, recovered)
		if err != nil {
			_ = f.Close()
			return nil, mapErr(err)
		}
		recoveredBuf, mErr := recovered.MarshalBinary()
		if mErr == nil {
			_, _ = f.WriteAt(recoveredBuf, 0)
			_ = f.Sync()
		}
		return &Container{file: f, opts: o, header: recovered, entries: entries}, nil
	}

	recovered, recErr = recoverHeaderFromDirectoryScan(f)
	if recErr == nil {
		entries, err := readEntriesByHeader(f, recovered)
		if err != nil {
			_ = f.Close()
			return nil, mapErr(err)
		}
		recoveredBuf, mErr := recovered.MarshalBinary()
		if mErr == nil {
			_, _ = f.WriteAt(recoveredBuf, 0)
			_ = f.Sync()
		}
		return &Container{file: f, opts: o, header: recovered, entries: entries}, nil
	}

	_ = f.Close()
	return nil, mapErr(recErr)
}

func Create(path string, opts *Options) (*Container, error) {
	o := defaultOptions()
	if opts != nil {
		o = mergeOptions(o, *opts)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	var h format.HeaderV1
	h.Magic = format.MagicHeader
	h.Version = format.VersionV1
	h.HeaderSize = format.HeaderSize
	h.CreatedUnix = uint64(time.Now().Unix())
	h.Reserved[0] = 1 // fixed-backup slot is reserved in this layout
	_, _ = rand.Read(h.UUID[:])

	headBuf, _ := h.MarshalBinary()
	if _, err := f.WriteAt(headBuf, 0); err != nil {
		_ = f.Close()
		return nil, err
	}

	dirBlob, crc, err := format.EncodeDirectory(format.DirectoryV1{BuildUnix: uint64(time.Now().Unix())})
	if err != nil {
		_ = f.Close()
		return nil, mapErr(err)
	}
	dirOffset := uint64(format.HeaderSize * 2)
	if _, err := f.WriteAt(dirBlob, int64(dirOffset)); err != nil {
		_ = f.Close()
		return nil, err
	}
	h.DirOffset = dirOffset
	h.DirSize = uint64(len(dirBlob))
	h.DirCRC32 = crc
	headBuf, _ = h.MarshalBinary()
	if _, err := f.WriteAt(headBuf, 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if o.SyncOnCommit {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return nil, err
		}
	}

	return &Container{file: f, opts: o, header: h, entries: map[string]format.EntryV1{}}, nil
}

func (c *Container) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil {
		return nil
	}
	err := c.file.Close()
	c.file = nil
	return err
}

func (c *Container) List() Iterator[EntryInfo] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	infos := make([]EntryInfo, 0, len(c.entries))
	for _, e := range c.entries {
		infos = append(infos, entryInfo(e))
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return newSliceIterator(infos, nil)
}

func (c *Container) Stat(path string) (EntryInfo, error) {
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return EntryInfo{}, ErrPathInvalid
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[norm]
	if !ok {
		return EntryInfo{}, ErrNotFound
	}
	return entryInfo(e), nil
}

func (c *Container) OpenReader(path string) (io.ReadCloser, error) {
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return nil, ErrPathInvalid
	}
	c.mu.RLock()
	e, ok := c.entries[norm]
	verifyCRC := c.opts.StrictVerify
	maxEntry := c.opts.MaxEntrySize
	maxChunk := c.opts.MaxChunkSize
	c.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if maxEntry > 0 && e.FileSize > maxEntry {
		return nil, ErrLimitExceeded
	}
	return &entryReader{
		f:         c.file,
		chunks:    append([]format.ChunkRefV1(nil), e.Chunks...),
		verifyCRC: verifyCRC,
		maxEntry:  maxEntry,
		maxChunk:  maxChunk,
	}, nil
}

func (c *Container) Extract(path string, w io.Writer) error {
	r, err := c.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(w, r)
	return err
}

func (c *Container) Add(path string, r io.Reader, meta []byte, wopts *WriteOptions) error {
	tx, err := c.Begin()
	if err != nil {
		return err
	}
	if err := tx.Add(path, r, meta, wopts); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Container) Upsert(path string, r io.Reader, meta []byte, wopts *WriteOptions) error {
	tx, err := c.Begin()
	if err != nil {
		return err
	}
	if err := tx.Upsert(path, r, meta, wopts); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Container) Replace(path string, r io.Reader, meta []byte, wopts *WriteOptions) error {
	tx, err := c.Begin()
	if err != nil {
		return err
	}
	if err := tx.Replace(path, r, meta, wopts); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Container) SetMeta(path string, meta []byte) error {
	tx, err := c.Begin()
	if err != nil {
		return err
	}
	if err := tx.SetMeta(path, meta); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Container) SetMetaMany(metaByPath map[string][]byte, ignoreMissing bool) (updated int, missing int, err error) {
	tx, err := c.Begin()
	if err != nil {
		return 0, 0, err
	}
	updated, missing, err = tx.SetMetaMany(metaByPath, ignoreMissing)
	if err != nil {
		tx.Rollback()
		return updated, missing, err
	}
	if updated == 0 {
		tx.Rollback()
		return 0, missing, nil
	}
	if err := tx.Commit(); err != nil {
		return updated, missing, err
	}
	return updated, missing, nil
}

func (c *Container) Remove(path string) error {
	tx, err := c.Begin()
	if err != nil {
		return err
	}
	if err := tx.Remove(path); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Container) Begin() (*Tx, error) {
	c.txMu.Lock()
	// c.file is protected by c.mu; read it under the same lock used by Close().
	c.mu.RLock()
	if c.file == nil {
		c.mu.RUnlock()
		c.txMu.Unlock()
		return nil, errors.New("fbx: container closed")
	}
	st, err := c.file.Stat()
	if err != nil {
		c.mu.RUnlock()
		c.txMu.Unlock()
		return nil, err
	}
	entries := make(map[string]format.EntryV1, len(c.entries))
	for k, v := range c.entries {
		entries[k] = cloneEntry(v)
	}
	c.mu.RUnlock()
	return &Tx{
		c:            c,
		entries:      entries,
		appendOffset: uint64(st.Size()),
	}, nil
}

func (c *Container) Verify(vopts *VerifyOptions) (*VerifyReport, error) {
	opts := VerifyOptions{Mode: VerifyDirectoryOnly}
	if vopts != nil {
		opts = *vopts
	}
	report := &VerifyReport{}

	c.mu.RLock()
	h := c.header
	entries := make([]format.EntryV1, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, cloneEntry(e))
	}
	c.mu.RUnlock()

	dirBlob, err := readDirectoryBlobByHeader(c.file, h)
	if err != nil {
		report.Errors = append(report.Errors, err)
		return report, errors.Join(report.Errors...)
	}
	if _, err := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize); err != nil {
		report.Errors = append(report.Errors, mapErr(err))
		return report, errors.Join(report.Errors...)
	}
	if opts.Mode == VerifyDirectoryOnly {
		return report, nil
	}

	for _, e := range entries {
		report.EntriesChecked++
		refs := e.Chunks
		if opts.Mode == VerifySampledChunks && len(refs) > 1 {
			refs = refs[:1]
		}
		for _, ref := range refs {
			report.ChunksChecked++
			rec, err := format.ReadChunkRecordAtOptLimited(c.file, int64(ref.ChunkOffset), true, ref.RawSize, ref.CompSize)
			if err != nil {
				report.Errors = append(report.Errors, mapErr(err))
				continue
			}
			if rec.RawSize != ref.RawSize || rec.CompSize != ref.CompSize || rec.CRC32Raw != ref.CRC32Raw {
				report.Errors = append(report.Errors, ErrInvalidFormat)
			}
		}
	}
	if len(report.Errors) > 0 {
		return report, errors.Join(report.Errors...)
	}
	return report, nil
}

type entryReader struct {
	f         io.ReaderAt
	chunks    []format.ChunkRefV1
	idx       int
	cur       []byte
	curRead   int
	verifyCRC bool
	maxEntry  uint64
	maxChunk  uint32
	readTotal uint64
}

func (r *entryReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := 0
	for n < len(p) {
		if r.curRead >= len(r.cur) {
			if r.idx >= len(r.chunks) {
				if n == 0 {
					return 0, io.EOF
				}
				return n, nil
			}
			// Do not increment r.idx yet: if any check below fails and n > 0,
			// we return the already-accumulated bytes and defer the error to the
			// next Read call (which will retry with the same r.idx).
			ref := r.chunks[r.idx]
			if r.maxChunk > 0 && ref.RawSize > r.maxChunk {
				if n > 0 {
					return n, nil
				}
				return 0, ErrLimitExceeded
			}
			rec, err := format.ReadChunkRecordAtOptLimited(r.f, int64(ref.ChunkOffset), r.verifyCRC, ref.RawSize, ref.CompSize)
			if err != nil {
				if n > 0 {
					return n, nil
				}
				return 0, mapErr(err)
			}
			if rec.RawSize != ref.RawSize || rec.CompSize != ref.CompSize || rec.CRC32Raw != ref.CRC32Raw {
				if n > 0 {
					return n, nil
				}
				return 0, ErrInvalidFormat
			}
			if r.maxEntry > 0 && r.readTotal+uint64(len(rec.Payload)) > r.maxEntry {
				if n > 0 {
					return n, nil
				}
				return 0, ErrLimitExceeded
			}
			// All checks passed; advance the index and load the payload.
			r.idx++
			r.cur = rec.Payload
			r.curRead = 0
		}
		copied := copy(p[n:], r.cur[r.curRead:])
		n += copied
		r.curRead += copied
		r.readTotal += uint64(copied)
	}
	return n, nil
}

func (r *entryReader) Close() error {
	r.cur = nil
	return nil
}

func entryInfo(e format.EntryV1) EntryInfo {
	meta := append([]byte(nil), e.Meta...)
	return EntryInfo{Path: e.Path, Size: e.FileSize, MTimeUnix: e.MTimeUnix, Mode: e.Mode, Flags: e.EntryFlags, Meta: meta}
}

func cloneEntry(e format.EntryV1) format.EntryV1 {
	out := e
	out.Chunks = append([]format.ChunkRefV1(nil), e.Chunks...)
	out.Meta = append([]byte(nil), e.Meta...)
	return out
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, format.ErrInvalidHeader) || errors.Is(err, format.ErrInvalidDir) || errors.Is(err, format.ErrInvalidChunk) {
		return ErrInvalidFormat
	}
	if errors.Is(err, format.ErrBadCodec) {
		return ErrUnsupportedCodec
	}
	if errors.Is(err, format.ErrLimitExceeded) {
		return ErrLimitExceeded
	}
	if errors.Is(err, format.ErrCRCMismatch) {
		return ErrCRCMismatch
	}
	if errors.Is(err, pathutil.ErrInvalidPath) {
		return ErrPathInvalid
	}
	return err
}

func mergeOptions(base, in Options) Options {
	out := base
	if in.ChunkSizeText > 0 {
		out.ChunkSizeText = in.ChunkSizeText
	}
	if in.ChunkSizeBin > 0 {
		out.ChunkSizeBin = in.ChunkSizeBin
	}
	if in.DefaultCodec == CodecStore || in.DefaultCodec == CodecZstd || in.DefaultCodec == CodecLZ4 {
		out.DefaultCodec = in.DefaultCodec
	}
	if in.DefaultLevel != 0 {
		out.DefaultLevel = in.DefaultLevel
	}
	if in.MaxWorkers > 0 {
		out.MaxWorkers = in.MaxWorkers
	}
	if in.MaxEntrySize > 0 {
		out.MaxEntrySize = in.MaxEntrySize
	}
	if in.MaxChunkSize > 0 {
		out.MaxChunkSize = in.MaxChunkSize
	}
	// Bool options that default to true cannot use the zero-value (false) to
	// express intent: Options{MaxWorkers: 4} would silently disable StrictVerify
	// and SyncOnCommit. Explicit opt-out is handled via the No* flags instead.
	if in.NoDetectText {
		out.DetectText = false
	}
	if in.NoStoreIfAlreadyCompressed {
		out.StoreIfAlreadyCompressed = false
	}
	if in.NoSyncOnCommit {
		out.SyncOnCommit = false
	}
	if in.NoStrictVerify {
		out.StrictVerify = false
	}
	return out
}

func appendAt(f *os.File, off uint64, data []byte) (uint64, error) {
	if len(data) == 0 {
		return off, fmt.Errorf("fbx: append zero data")
	}
	if _, err := f.WriteAt(data, int64(off)); err != nil {
		return off, err
	}
	return off + uint64(len(data)), nil
}

func validateHeaderDirectory(f *os.File, h format.HeaderV1) error {
	dirBlob, err := readDirectoryBlobByHeader(f, h)
	if err != nil {
		return err
	}
	_, err = format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize)
	return err
}

func readEntriesByHeader(f *os.File, h format.HeaderV1) (map[string]format.EntryV1, error) {
	dirBlob, err := readDirectoryBlobByHeader(f, h)
	if err != nil {
		return nil, mapErr(err)
	}
	dir, err := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize)
	if err != nil {
		return nil, mapErr(err)
	}
	entries := make(map[string]format.EntryV1, len(dir.Entries))
	for _, e := range dir.Entries {
		if _, ok := entries[e.Path]; ok {
			return nil, ErrInvalidFormat
		}
		entries[e.Path] = cloneEntry(e)
	}
	return entries, nil
}

func readDirectoryBlobByHeader(f *os.File, h format.HeaderV1) ([]byte, error) {
	if h.DirOffset == 0 || h.DirSize == 0 {
		return nil, ErrInvalidFormat
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := uint64(st.Size())
	if !regionWithinFile(h.DirOffset, h.DirSize, fileSize) {
		return nil, ErrInvalidFormat
	}
	if h.DirSize > maxDirectoryBlobSize {
		return nil, ErrLimitExceeded
	}
	maxInt := uint64(int(^uint(0) >> 1))
	if h.DirSize > maxInt {
		return nil, ErrLimitExceeded
	}
	dirBlob := make([]byte, int(h.DirSize))
	if _, err := f.ReadAt(dirBlob, int64(h.DirOffset)); err != nil {
		return nil, err
	}
	return dirBlob, nil
}

func regionWithinFile(offset, size, fileSize uint64) bool {
	if offset > fileSize {
		return false
	}
	return size <= fileSize-offset
}

func addUint64Checked(a, b uint64) (uint64, bool) {
	if a > ^uint64(0)-b {
		return 0, false
	}
	return a + b, true
}

func mulUint64Checked(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > ^uint64(0)/b {
		return 0, false
	}
	return a * b, true
}
