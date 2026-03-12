package fbx

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pixfid/go-fbx/internal/format"
	"github.com/pixfid/go-fbx/internal/pathutil"
)

type Container struct {
	file          *os.File
	path          string
	opts          Options
	txMu          sync.Mutex
	mu            sync.RWMutex
	formatVersion uint16
	header        format.HeaderV1
	entries       map[string]format.EntryV1
	lazyDirBlob   []byte
	lazyDirIndex  *format.DirIndexV1
}

const maxDirectoryBlobSize uint64 = 1 << 30 // 1 GiB hard cap for directory blob reads.

func Open(path string, opts *Options) (*Container, error) {
	o := defaultOptions()
	if opts != nil {
		o = mergeOptions(o, *opts)
	}
	absPath := path
	if ap, aerr := filepath.Abs(path); aerr == nil {
		absPath = ap
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
	var openErr error
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err == nil {
		snap, err := loadSnapshotByHeader(f, h, true)
		if err == nil {
			return buildContainerFromSnapshot(f, absPath, o, h, snap), nil
		}
		openErr = mapErr(err)
		if !shouldAttemptOpenRecovery(openErr) {
			_ = f.Close()
			return nil, openErr
		}
	} else {
		openErr = mapErr(err)
	}

	tryRecovered := func(recovered format.HeaderV1) (*Container, error) {
		snap, rerr := loadSnapshotByHeader(f, recovered, true)
		if rerr != nil {
			rerr = mapErr(rerr)
			if shouldAttemptOpenRecovery(rerr) {
				if openErr == nil {
					openErr = rerr
				}
				return nil, nil
			}
			return nil, rerr
		}
		persistRecoveredHeader(f, recovered, absPath)
		return buildContainerFromSnapshot(f, absPath, o, recovered, snap), nil
	}

	recovered, recErr := recoverHeaderFromFixedBackup(f)
	if recErr == nil {
		c, fatal := tryRecovered(recovered)
		if fatal != nil {
			_ = f.Close()
			return nil, fatal
		}
		if c != nil {
			c.formatVersion = format.VersionV1
			return c, nil
		}
	} else if openErr == nil {
		openErr = mapErr(recErr)
	}

	recovered, recErr = recoverBestHeader(f)
	if recErr == nil {
		c, fatal := tryRecovered(recovered)
		if fatal != nil {
			_ = f.Close()
			return nil, fatal
		}
		if c != nil {
			c.formatVersion = format.VersionV1
			return c, nil
		}
	} else if openErr == nil {
		openErr = mapErr(recErr)
	}

	recovered, recErr = recoverHeaderFromDirectoryScan(f)
	if recErr == nil {
		c, fatal := tryRecovered(recovered)
		if fatal != nil {
			_ = f.Close()
			return nil, fatal
		}
		if c != nil {
			c.formatVersion = format.VersionV1
			return c, nil
		}
	} else if openErr == nil {
		openErr = mapErr(recErr)
	}

	_ = f.Close()
	if openErr != nil {
		return nil, openErr
	}
	return nil, mapErr(recErr)
}

type containerSnapshot struct {
	entries  map[string]format.EntryV1
	dirBlob  []byte
	dirIndex *format.DirIndexV1
}

func buildContainerFromSnapshot(f *os.File, path string, opts Options, h format.HeaderV1, snap containerSnapshot) *Container {
	return &Container{
		file:          f,
		path:          path,
		opts:          opts,
		formatVersion: format.VersionV1,
		header:        h,
		entries:       snap.entries,
		lazyDirBlob:   snap.dirBlob,
		lazyDirIndex:  snap.dirIndex,
	}
}

func Create(path string, opts *Options) (*Container, error) {
	o := defaultOptions()
	if opts != nil {
		o = mergeOptions(o, *opts)
	}
	absPath := path
	if ap, aerr := filepath.Abs(path); aerr == nil {
		absPath = ap
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
	markHeaderV1ExtensionLayout(&h)
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

	return &Container{file: f, path: absPath, opts: o, formatVersion: format.VersionV1, header: h, entries: map[string]format.EntryV1{}}, nil
}

func (c *Container) Close() error {
	if !c.txMu.TryLock() {
		return errors.New("fbx: transaction active")
	}
	defer c.txMu.Unlock()
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
	c.mu.Lock()
	if c.file == nil {
		c.mu.Unlock()
		return newSliceIterator[EntryInfo](nil, errors.New("fbx: container closed"))
	}
	if err := c.ensureEntriesLoadedLocked(); err != nil {
		c.mu.Unlock()
		return newSliceIterator[EntryInfo](nil, err)
	}
	infos := make([]EntryInfo, 0, len(c.entries))
	for _, e := range c.entries {
		infos = append(infos, entryInfo(e))
	}
	c.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return newSliceIterator(infos, nil)
}

func (c *Container) Stat(path string) (EntryInfo, error) {
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return EntryInfo{}, ErrPathInvalid
	}
	c.mu.RLock()
	if c.file == nil {
		c.mu.RUnlock()
		return EntryInfo{}, errors.New("fbx: container closed")
	}
	if c.entries != nil {
		e, ok := c.entries[norm]
		c.mu.RUnlock()
		if !ok {
			return EntryInfo{}, ErrNotFound
		}
		return entryInfo(e), nil
	}
	if c.lazyDirIndex != nil {
		e, ok, err := findEntryByPathFromDirIndex(c.lazyDirBlob, c.lazyDirIndex, norm)
		c.mu.RUnlock()
		if err != nil {
			return EntryInfo{}, err
		}
		if !ok {
			return EntryInfo{}, ErrNotFound
		}
		return entryInfo(e), nil
	}
	c.mu.RUnlock()
	return EntryInfo{}, ErrInvalidFormat
}

func (c *Container) OpenReader(path string) (io.ReadCloser, error) {
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return nil, ErrPathInvalid
	}
	c.mu.RLock()
	file := c.file
	verifyCRC := c.opts.StrictVerify
	maxEntry := c.opts.MaxEntrySize
	maxChunk := c.opts.MaxChunkSize
	if file == nil {
		c.mu.RUnlock()
		return nil, errors.New("fbx: container closed")
	}
	var (
		e    format.EntryV1
		ok   bool
		lerr error
	)
	if c.entries != nil {
		e, ok = c.entries[norm]
	} else if c.lazyDirIndex != nil {
		e, ok, lerr = findEntryByPathFromDirIndex(c.lazyDirBlob, c.lazyDirIndex, norm)
	} else {
		lerr = ErrInvalidFormat
	}
	c.mu.RUnlock()
	if lerr != nil {
		return nil, lerr
	}
	if !ok {
		return nil, ErrNotFound
	}
	if maxEntry > 0 && e.FileSize > maxEntry {
		return nil, ErrLimitExceeded
	}
	return &entryReader{
		f:         file,
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
	if c.formatVersion != format.VersionV1 {
		c.txMu.Unlock()
		return nil, ErrUnsupportedFeature
	}
	// c.file is protected by c.mu; read it under the same lock used by Close().
	c.mu.Lock()
	if c.file == nil {
		c.mu.Unlock()
		c.txMu.Unlock()
		return nil, errors.New("fbx: container closed")
	}
	unlockProcess := acquireProcessWriteLock(c.path)
	if err := lockFileExclusive(c.file); err != nil {
		unlockProcess()
		c.mu.Unlock()
		c.txMu.Unlock()
		return nil, err
	}
	if err := c.ensureEntriesLoadedLocked(); err != nil {
		_ = unlockFile(c.file)
		unlockProcess()
		c.mu.Unlock()
		c.txMu.Unlock()
		return nil, err
	}
	st, err := c.file.Stat()
	if err != nil {
		_ = unlockFile(c.file)
		unlockProcess()
		c.mu.Unlock()
		c.txMu.Unlock()
		return nil, err
	}
	entries := make(map[string]format.EntryV1, len(c.entries))
	for k, v := range c.entries {
		entries[k] = cloneEntry(v)
	}
	c.mu.Unlock()
	return &Tx{
		c:               c,
		entries:         entries,
		appendOffset:    uint64(st.Size()),
		unlockWritePath: unlockProcess,
		fileWriteLocked: true,
	}, nil
}

func persistRecoveredHeader(f *os.File, recovered format.HeaderV1, path string) {
	recoveredBuf, mErr := recovered.MarshalBinary()
	if mErr != nil {
		return
	}
	unlockProcess := acquireProcessWriteLock(path)
	defer unlockProcess()
	if err := lockFileExclusive(f); err != nil {
		return
	}
	defer func() { _ = unlockFile(f) }()
	_, _ = f.WriteAt(recoveredBuf, 0)
	if headerHasFixedBackupSlot(recovered) {
		_, _ = f.WriteAt(recoveredBuf, fixedBackupOffset)
	}
	_ = f.Sync()
}

func (c *Container) Verify(vopts *VerifyOptions) (*VerifyReport, error) {
	opts := VerifyOptions{Mode: VerifyDirectoryOnly}
	if vopts != nil {
		opts = *vopts
	}
	report := &VerifyReport{}

	c.mu.Lock()
	h := c.header
	formatVersion := c.formatVersion
	maxChunk := c.opts.MaxChunkSize
	if err := c.ensureEntriesLoadedLocked(); err != nil {
		c.mu.Unlock()
		report.Errors = append(report.Errors, err)
		return report, errors.Join(report.Errors...)
	}
	entries := make([]format.EntryV1, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, cloneEntry(e))
	}
	c.mu.Unlock()

	if formatVersion == format.VersionV1 {
		dirBlob, err := readDirectoryBlobByHeader(c.file, h)
		if err != nil {
			report.Errors = append(report.Errors, err)
			return report, errors.Join(report.Errors...)
		}
		if _, err := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize); err != nil {
			report.Errors = append(report.Errors, mapErr(err))
			return report, errors.Join(report.Errors...)
		}
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
			if maxChunk > 0 && (ref.RawSize > maxChunk || ref.CompSize > maxChunk) {
				report.Errors = append(report.Errors, ErrLimitExceeded)
				continue
			}
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
			if r.maxChunk > 0 && (ref.RawSize > r.maxChunk || ref.CompSize > r.maxChunk) {
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
	snap, err := loadSnapshotByHeader(f, h, false)
	if err != nil {
		return nil, err
	}
	return snap.entries, nil
}

func loadSnapshotByHeader(f *os.File, h format.HeaderV1, preferLazy bool) (containerSnapshot, error) {
	if err := validateHeaderRequiredFeatures(h); err != nil {
		return containerSnapshot{}, err
	}
	dirBlob, err := readDirectoryBlobByHeader(f, h)
	if err != nil {
		return containerSnapshot{}, err
	}
	st, err := f.Stat()
	if err != nil {
		return containerSnapshot{}, err
	}
	fileSize := uint64(st.Size())

	var entries map[string]format.EntryV1
	if idx, ok, idxErr := maybeReadDirIndexByHeader(f, h, fileSize); idxErr != nil {
		return containerSnapshot{}, idxErr
	} else if ok {
		if preferLazy {
			entryCount, envErr := validateDirectoryBlobEnvelope(dirBlob, h.DirCRC32, h.DirSize)
			if envErr != nil {
				return containerSnapshot{}, envErr
			}
			if int(entryCount) != len(idx.Entries) {
				return containerSnapshot{}, ErrInvalidFormat
			}
			idxCopy := idx
			return containerSnapshot{
				dirBlob:  dirBlob,
				dirIndex: &idxCopy,
			}, nil
		}
		entries, err = readEntriesByDirIndex(dirBlob, h, idx)
		if err != nil {
			return containerSnapshot{}, err
		}
	} else {
		dir, derr := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize)
		if derr != nil {
			return containerSnapshot{}, mapErr(derr)
		}
		entries = make(map[string]format.EntryV1, len(dir.Entries))
		for _, e := range dir.Entries {
			if _, ok := entries[e.Path]; ok {
				return containerSnapshot{}, ErrInvalidFormat
			}
			entries[e.Path] = cloneEntry(e)
		}
	}

	if err := validateEntriesChunkOffsets(entries, fileSize); err != nil {
		return containerSnapshot{}, err
	}
	return containerSnapshot{
		entries: entries,
	}, nil
}

func findEntryByPathFromDirIndex(dirBlob []byte, idx *format.DirIndexV1, path string) (format.EntryV1, bool, error) {
	if idx == nil {
		return format.EntryV1{}, false, nil
	}
	hash := format.FNV1a64(path)
	ranges := idx.HashRanges
	pos := sort.Search(len(ranges), func(i int) bool {
		return ranges[i].PathHash64 >= hash
	})
	for i := pos; i < len(ranges) && ranges[i].PathHash64 == hash; i++ {
		row := ranges[i]
		end, ok := addUint64Checked(uint64(row.FirstEntryIndex), uint64(row.EntrySpan))
		if !ok || end > uint64(len(idx.Entries)) {
			return format.EntryV1{}, false, ErrInvalidFormat
		}
		for j := row.FirstEntryIndex; j < uint32(end); j++ {
			eRow := idx.Entries[j]
			e, err := format.DecodeDirectoryEntryAt(dirBlob, eRow.DirEntryOffset, eRow.DirEntrySize)
			if err != nil {
				return format.EntryV1{}, false, mapErr(err)
			}
			if e.Path == path {
				return e, true, nil
			}
		}
	}
	return format.EntryV1{}, false, nil
}

func (c *Container) ensureEntriesLoadedLocked() error {
	if c.entries != nil {
		return nil
	}
	if c.file == nil {
		return errors.New("fbx: container closed")
	}
	if c.lazyDirIndex != nil {
		entries, err := readEntriesByDirIndex(c.lazyDirBlob, c.header, *c.lazyDirIndex)
		if err != nil {
			return err
		}
		st, err := c.file.Stat()
		if err != nil {
			return err
		}
		if err := validateEntriesChunkOffsets(entries, uint64(st.Size())); err != nil {
			return err
		}
		c.entries = entries
		c.lazyDirBlob = nil
		c.lazyDirIndex = nil
		return nil
	}
	entries, err := readEntriesByHeader(c.file, c.header)
	if err != nil {
		return err
	}
	c.entries = entries
	return nil
}

func validateEntriesChunkOffsets(entries map[string]format.EntryV1, fileSize uint64) error {
	for _, e := range entries {
		for _, ref := range e.Chunks {
			chunkEnd, ok := addUint64Checked(ref.ChunkOffset, 16)
			if !ok {
				return ErrInvalidFormat
			}
			chunkEnd, ok = addUint64Checked(chunkEnd, uint64(ref.CompSize))
			if !ok || chunkEnd > fileSize {
				return ErrInvalidFormat
			}
		}
	}
	return nil
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

func shouldAttemptOpenRecovery(err error) bool {
	return errors.Is(err, ErrInvalidFormat) || errors.Is(err, ErrCRCMismatch)
}
