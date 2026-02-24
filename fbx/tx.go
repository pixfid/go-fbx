package fbx

import (
	"bufio"
	"bytes"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pixfid/go-fbx/internal/format"
	"github.com/pixfid/go-fbx/internal/pathutil"
)

type Tx struct {
	c            *Container
	entries      map[string]format.EntryV1
	appendOffset uint64
	deadBytes    uint64
	churnOps     uint64
	closed       bool
}

func (tx *Tx) Add(path string, r io.Reader, meta []byte, wopts *WriteOptions) error {
	if tx.closed {
		return io.ErrClosedPipe
	}
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return ErrPathInvalid
	}
	if _, ok := tx.entries[norm]; ok {
		return ErrAlreadyExists
	}
	e, err := tx.writeEntry(norm, r, meta, wopts)
	if err != nil {
		return err
	}
	tx.entries[norm] = e
	return nil
}

func (tx *Tx) Upsert(path string, r io.Reader, meta []byte, wopts *WriteOptions) error {
	if tx.closed {
		return io.ErrClosedPipe
	}
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return ErrPathInvalid
	}
	if old, ok := tx.entries[norm]; ok {
		tx.markObsolete(old)
	}
	e, err := tx.writeEntry(norm, r, meta, wopts)
	if err != nil {
		return err
	}
	tx.entries[norm] = e
	return nil
}

func (tx *Tx) Replace(path string, r io.Reader, meta []byte, wopts *WriteOptions) error {
	if tx.closed {
		return io.ErrClosedPipe
	}
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return ErrPathInvalid
	}
	old, ok := tx.entries[norm]
	if !ok {
		return ErrNotFound
	}
	tx.markObsolete(old)
	e, err := tx.writeEntry(norm, r, meta, wopts)
	if err != nil {
		return err
	}
	tx.entries[norm] = e
	return nil
}

func (tx *Tx) Remove(path string) error {
	if tx.closed {
		return io.ErrClosedPipe
	}
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return ErrPathInvalid
	}
	old, ok := tx.entries[norm]
	if !ok {
		return ErrNotFound
	}
	tx.markObsolete(old)
	delete(tx.entries, norm)
	return nil
}

func (tx *Tx) RemoveMany(paths []string) (int, error) {
	if tx.closed {
		return 0, io.ErrClosedPipe
	}
	removed := 0
	for _, p := range paths {
		norm, err := pathutil.Normalize(p)
		if err != nil {
			return removed, ErrPathInvalid
		}
		if old, ok := tx.entries[norm]; ok {
			tx.markObsolete(old)
			delete(tx.entries, norm)
			removed++
		}
	}
	return removed, nil
}

func (tx *Tx) RemovePrefix(prefix string) (int, error) {
	if tx.closed {
		return 0, io.ErrClosedPipe
	}
	norm, err := normalizePrefix(prefix)
	if err != nil {
		return 0, ErrPathInvalid
	}
	removed := 0
	fullPrefix := norm + "/"
	for p := range tx.entries {
		if p == norm || strings.HasPrefix(p, fullPrefix) {
			tx.markObsolete(tx.entries[p])
			delete(tx.entries, p)
			removed++
		}
	}
	return removed, nil
}

func (tx *Tx) RemoveGlob(glob string) (int, error) {
	if tx.closed {
		return 0, io.ErrClosedPipe
	}
	glob = strings.ReplaceAll(glob, "\\", "/")
	glob = strings.TrimLeft(glob, "/")
	if glob == "" {
		return 0, ErrPathInvalid
	}
	// Validate glob syntax once.
	if _, err := path.Match(glob, "x"); err != nil {
		return 0, ErrPathInvalid
	}
	removed := 0
	for p := range tx.entries {
		ok, err := path.Match(glob, p)
		if err != nil {
			return removed, ErrPathInvalid
		}
		if ok {
			tx.markObsolete(tx.entries[p])
			delete(tx.entries, p)
			removed++
		}
	}
	return removed, nil
}

func (tx *Tx) RemoveWhere(predicate func(EntryInfo) bool) (int, error) {
	if tx.closed {
		return 0, io.ErrClosedPipe
	}
	if predicate == nil {
		return 0, ErrInvalidFormat
	}
	removed := 0
	for p, e := range tx.entries {
		if predicate(entryInfo(e)) {
			tx.markObsolete(e)
			delete(tx.entries, p)
			removed++
		}
	}
	return removed, nil
}

// SetMeta updates entry metadata without rewriting entry payload chunks.
func (tx *Tx) SetMeta(path string, meta []byte) error {
	if tx.closed {
		return io.ErrClosedPipe
	}
	norm, err := pathutil.Normalize(path)
	if err != nil {
		return ErrPathInvalid
	}
	e, ok := tx.entries[norm]
	if !ok {
		return ErrNotFound
	}
	e.Meta = append([]byte(nil), meta...)
	tx.entries[norm] = e
	return nil
}

// SetMetaMany updates metadata for many entries in one transaction.
// When ignoreMissing is false, missing paths fail the operation.
func (tx *Tx) SetMetaMany(metaByPath map[string][]byte, ignoreMissing bool) (updated int, missing int, err error) {
	if tx.closed {
		return 0, 0, io.ErrClosedPipe
	}
	for p, meta := range metaByPath {
		norm, nerr := pathutil.Normalize(p)
		if nerr != nil {
			return updated, missing, ErrPathInvalid
		}
		e, ok := tx.entries[norm]
		if !ok {
			if ignoreMissing {
				missing++
				continue
			}
			return updated, missing, ErrNotFound
		}
		e.Meta = append([]byte(nil), meta...)
		tx.entries[norm] = e
		updated++
	}
	return updated, missing, nil
}

func (tx *Tx) Commit() error {
	if tx.closed {
		return io.ErrClosedPipe
	}
	entries := make([]format.EntryV1, 0, len(tx.entries))
	for _, e := range tx.entries {
		entries = append(entries, cloneEntry(e))
	}
	dirBlob, crc, err := format.EncodeDirectory(format.DirectoryV1{BuildUnix: uint64(time.Now().Unix()), Entries: entries})
	if err != nil {
		tx.release()
		return mapErr(err)
	}

	dirOffset := tx.appendOffset
	tx.appendOffset, err = appendAt(tx.c.file, tx.appendOffset, dirBlob)
	if err != nil {
		tx.release()
		return err
	}
	if tx.c.opts.SyncOnCommit {
		if err := tx.c.file.Sync(); err != nil {
			tx.release()
			return err
		}
	}

	tx.c.mu.RLock()
	h := tx.c.header
	tx.c.mu.RUnlock()
	deadBytes, churnOps := readCompactionStatsFromHeader(h)
	deadBytes = saturatingAddUint64(deadBytes, tx.deadBytes)
	churnOps = saturatingAddUint64(churnOps, tx.churnOps)
	writeCompactionStatsToHeader(&h, deadBytes, churnOps)
	h.DirOffset = dirOffset
	h.DirSize = uint64(len(dirBlob))
	h.DirCRC32 = crc

	h.Flags |= format.HeaderFlagHasJournal
	h.Flags |= format.HeaderFlagHasBackup
	h.JournalOffset = tx.appendOffset
	h.JournalSize = journalRecordSize
	headBuf, err := h.MarshalBinary()
	if err != nil {
		tx.release()
		return mapErr(err)
	}
	journalRec, err := buildJournalRecord(headBuf, uint64(time.Now().Unix()))
	if err != nil {
		tx.release()
		return mapErr(err)
	}
	tx.appendOffset, err = appendAt(tx.c.file, tx.appendOffset, journalRec)
	if err != nil {
		tx.release()
		return err
	}
	backupRec, err := buildBackupRecord(headBuf, uint64(time.Now().Unix()))
	if err != nil {
		tx.release()
		return mapErr(err)
	}
	tx.appendOffset, err = appendAt(tx.c.file, tx.appendOffset, backupRec)
	if err != nil {
		tx.release()
		return err
	}
	if tx.c.opts.SyncOnCommit {
		if err := tx.c.file.Sync(); err != nil {
			tx.release()
			return err
		}
	}

	// Fixed backup header slot at offset 128 for containers created with the reserved slot layout.
	if tx.c.header.Reserved[0] == 1 {
		if _, err := tx.c.file.WriteAt(headBuf, fixedBackupOffset); err != nil {
			tx.release()
			return err
		}
		if tx.c.opts.SyncOnCommit {
			if err := tx.c.file.Sync(); err != nil {
				tx.release()
				return err
			}
		}
	}

	if _, err := tx.c.file.WriteAt(headBuf, 0); err != nil {
		tx.release()
		return err
	}
	if tx.c.opts.SyncOnCommit {
		if err := tx.c.file.Sync(); err != nil {
			tx.release()
			return err
		}
	}

	tx.c.mu.Lock()
	tx.c.header = h
	tx.c.entries = make(map[string]format.EntryV1, len(tx.entries))
	for k, v := range tx.entries {
		tx.c.entries[k] = cloneEntry(v)
	}
	tx.c.mu.Unlock()

	tx.release()
	return nil
}

func (tx *Tx) Rollback() {
	if tx.closed {
		return
	}
	tx.release()
}

func (tx *Tx) release() {
	tx.closed = true
	tx.c.txMu.Unlock()
}

func (tx *Tx) markObsolete(e format.EntryV1) {
	tx.deadBytes = saturatingAddUint64(tx.deadBytes, entryStoredBytes(e))
	tx.churnOps = saturatingAddUint64(tx.churnOps, 1)
}

func entryStoredBytes(e format.EntryV1) uint64 {
	var total uint64
	for _, ch := range e.Chunks {
		total = saturatingAddUint64(total, uint64(ch.CompSize)+16)
	}
	return total
}

func (tx *Tx) writeEntry(path string, r io.Reader, meta []byte, wopts *WriteOptions) (format.EntryV1, error) {
	if r == nil {
		return format.EntryV1{}, ErrInvalidFormat
	}
	br := bufio.NewReaderSize(r, 64<<10)
	probe, _ := br.Peek(4096)

	chunkSize := tx.c.opts.ChunkSizeBin
	codec := tx.c.opts.DefaultCodec
	level := tx.c.opts.DefaultLevel
	var mtime uint64 = uint64(time.Now().Unix())
	var mode uint32
	var flags uint32
	explicitChunkSize := false
	explicitCodec := false
	if wopts != nil {
		if wopts.ChunkSize > 0 {
			chunkSize = wopts.ChunkSize
			explicitChunkSize = true
		}
		// Non-store values are treated as explicit codec choice.
		if wopts.Codec == CodecZstd || wopts.Codec == CodecLZ4 {
			explicitCodec = true
			codec = wopts.Codec
		} else if wopts.Codec == CodecStore {
			codec = CodecStore
		}
		if wopts.Level != 0 {
			level = wopts.Level
		}
		if wopts.MTimeUnix != 0 {
			mtime = wopts.MTimeUnix
		}
		mode = wopts.Mode
		flags = wopts.Flags
	}
	isText := false
	if flags&EntryFlagIsText != 0 {
		isText = true
	} else if flags&EntryFlagIsBinary != 0 {
		isText = false
	} else if tx.c.opts.DetectText {
		isText = detectText(path, probe)
		if isText {
			flags |= EntryFlagIsText
		} else {
			flags |= EntryFlagIsBinary
		}
	}
	if !explicitChunkSize {
		if isText && tx.c.opts.ChunkSizeText > 0 {
			chunkSize = tx.c.opts.ChunkSizeText
		} else if tx.c.opts.ChunkSizeBin > 0 {
			chunkSize = tx.c.opts.ChunkSizeBin
		}
	}
	if tx.c.opts.StoreIfAlreadyCompressed && !explicitCodec && codec != CodecStore && looksCompressed(path, probe) {
		codec = CodecStore
	}
	if chunkSize <= 0 {
		chunkSize = 4 << 20
	}
	if tx.c.opts.MaxChunkSize > 0 {
		if chunkSize > int(tx.c.opts.MaxChunkSize) {
			chunkSize = int(tx.c.opts.MaxChunkSize)
		}
		if chunkSize <= 0 {
			return format.EntryV1{}, ErrLimitExceeded
		}
	}
	workers := tx.c.opts.MaxWorkers
	if workers <= 0 {
		workers = 1
	}

	chunks, fileSize, err := tx.writeChunks(br, format.Codec(codec), uint8(level), chunkSize, workers, tx.c.opts.MaxEntrySize)
	if err != nil {
		return format.EntryV1{}, err
	}

	e := format.EntryV1{
		Path:       path,
		MTimeUnix:  mtime,
		Mode:       mode,
		EntryFlags: flags,
		FileSize:   fileSize,
		Chunks:     chunks,
		Meta:       append([]byte(nil), meta...),
	}
	return e, nil
}

func detectText(path string, probe []byte) bool {
	if textExt(path) {
		return true
	}
	if len(probe) == 0 {
		return false
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return false
	}
	if !utf8.Valid(probe) {
		return false
	}
	printable := 0
	for _, b := range probe {
		if b == '\n' || b == '\r' || b == '\t' || (b >= 32 && b <= 126) {
			printable++
		}
	}
	return printable*100/len(probe) >= 85
}

func textExt(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".txt", ".md", ".json", ".xml", ".html", ".htm", ".csv", ".yaml", ".yml", ".fb2", ".opf", ".css", ".js":
		return true
	default:
		return false
	}
}

func looksCompressed(path string, probe []byte) bool {
	if compressedExt(path) {
		return true
	}
	if len(probe) >= 2 {
		// JPEG
		if probe[0] == 0xff && probe[1] == 0xd8 {
			return true
		}
		// gzip
		if probe[0] == 0x1f && probe[1] == 0x8b {
			return true
		}
	}
	if len(probe) >= 4 {
		// PNG
		if probe[0] == 0x89 && probe[1] == 0x50 && probe[2] == 0x4e && probe[3] == 0x47 {
			return true
		}
		// ZIP
		if probe[0] == 0x50 && probe[1] == 0x4b && (probe[2] == 0x03 || probe[2] == 0x05 || probe[2] == 0x07) && (probe[3] == 0x04 || probe[3] == 0x06 || probe[3] == 0x08) {
			return true
		}
		// WebP (RIFF....WEBP)
		if len(probe) >= 12 && probe[0] == 'R' && probe[1] == 'I' && probe[2] == 'F' && probe[3] == 'F' &&
			probe[8] == 'W' && probe[9] == 'E' && probe[10] == 'B' && probe[11] == 'P' {
			return true
		}
	}
	return false
}

func compressedExt(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".zip", ".gz", ".bz2", ".xz", ".7z", ".mp3", ".m4a", ".aac", ".ogg", ".mp4", ".mkv", ".pdf":
		return true
	default:
		return false
	}
}

type chunkJob struct {
	index  int
	rawOff uint64
	raw    []byte
}

type chunkResult struct {
	index  int
	rawOff uint64
	rawSz  uint32
	rec    []byte
	crc    uint32
	err    error
}

func (tx *Tx) writeChunks(r io.Reader, codec format.Codec, level uint8, chunkSize, workers int, maxEntry uint64) ([]format.ChunkRefV1, uint64, error) {
	if workers <= 1 {
		return tx.writeChunksSequential(r, codec, level, chunkSize, maxEntry)
	}
	return tx.writeChunksParallel(r, codec, level, chunkSize, workers, maxEntry)
}

func (tx *Tx) writeChunksSequential(r io.Reader, codec format.Codec, level uint8, chunkSize int, maxEntry uint64) ([]format.ChunkRefV1, uint64, error) {
	buf := make([]byte, chunkSize)
	var chunks []format.ChunkRefV1
	var rawOff uint64

	for {
		n, readErr := io.ReadFull(r, buf)
		if readErr == io.EOF {
			break
		}
		if readErr == io.ErrUnexpectedEOF {
			if n == 0 {
				break
			}
		} else if readErr != nil {
			return nil, rawOff, readErr
		}

		if maxEntry > 0 && rawOff+uint64(n) > maxEntry {
			return nil, rawOff, ErrLimitExceeded
		}
		rec, crc, err := format.EncodeChunkRecord(buf[:n], codec, level)
		if err != nil {
			return nil, rawOff, mapErr(err)
		}
		chunkOffset := tx.appendOffset
		tx.appendOffset, err = appendAt(tx.c.file, tx.appendOffset, rec)
		if err != nil {
			return nil, rawOff, err
		}
		chunks = append(chunks, format.ChunkRefV1{
			ChunkOffset: chunkOffset,
			RawOffset:   rawOff,
			RawSize:     uint32(n),
			CompSize:    uint32(len(rec) - 16),
			CRC32Raw:    crc,
		})
		rawOff += uint64(n)
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	return chunks, rawOff, nil
}

func (tx *Tx) writeChunksParallel(r io.Reader, codec format.Codec, level uint8, chunkSize, workers int, maxEntry uint64) ([]format.ChunkRefV1, uint64, error) {
	jobs := make(chan chunkJob, workers*2)
	results := make(chan chunkResult, workers*2)
	produceErrCh := make(chan error, 1)
	rawTotalCh := make(chan uint64, 1)
	bufPool := sync.Pool{
		New: func() any {
			return make([]byte, chunkSize)
		},
	}

	var workerWG sync.WaitGroup
	for i := 0; i < workers; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for j := range jobs {
				rec, crc, err := format.EncodeChunkRecord(j.raw, codec, level)
				bufPool.Put(j.raw[:chunkSize])
				if err != nil {
					results <- chunkResult{index: j.index, rawOff: j.rawOff, err: mapErr(err)}
					continue
				}
				results <- chunkResult{
					index:  j.index,
					rawOff: j.rawOff,
					rawSz:  uint32(len(j.raw)),
					rec:    rec,
					crc:    crc,
				}
			}
		}()
	}

	go func() {
		workerWG.Wait()
		close(results)
	}()

	go func() {
		defer close(jobs)
		idx := 0
		var rawOff uint64
		var produceErr error
		for {
			buf := bufPool.Get().([]byte)
			n, readErr := io.ReadFull(r, buf)
			if readErr == io.EOF {
				bufPool.Put(buf)
				break
			}
			if readErr == io.ErrUnexpectedEOF {
				if n == 0 {
					bufPool.Put(buf)
					break
				}
			} else if readErr != nil {
				bufPool.Put(buf)
				produceErr = readErr
				break
			}
			raw := buf[:n]
			if maxEntry > 0 && rawOff+uint64(n) > maxEntry {
				bufPool.Put(buf)
				produceErr = ErrLimitExceeded
				break
			}
			jobs <- chunkJob{index: idx, rawOff: rawOff, raw: raw}
			rawOff += uint64(n)
			idx++
			if readErr == io.ErrUnexpectedEOF {
				break
			}
		}
		produceErrCh <- produceErr
		rawTotalCh <- rawOff
	}()

	pending := map[int]chunkResult{}
	next := 0
	var chunks []format.ChunkRefV1
	var outErr error

	for res := range results {
		if res.err != nil && outErr == nil {
			outErr = res.err
		}
		pending[res.index] = res
		for {
			cur, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++
			if cur.err != nil {
				if outErr == nil {
					outErr = cur.err
				}
				continue
			}
			if outErr != nil {
				continue
			}
			chunkOffset := tx.appendOffset
			var err error
			tx.appendOffset, err = appendAt(tx.c.file, tx.appendOffset, cur.rec)
			if err != nil {
				outErr = err
				continue
			}
			chunks = append(chunks, format.ChunkRefV1{
				ChunkOffset: chunkOffset,
				RawOffset:   cur.rawOff,
				RawSize:     cur.rawSz,
				CompSize:    uint32(len(cur.rec) - 16),
				CRC32Raw:    cur.crc,
			})
		}
	}

	produceErr := <-produceErrCh
	rawTotal := <-rawTotalCh
	if outErr != nil {
		return nil, rawTotal, outErr
	}
	if produceErr != nil {
		return nil, rawTotal, produceErr
	}
	return chunks, rawTotal, nil
}

func normalizePrefix(prefix string) (string, error) {
	prefix = strings.ReplaceAll(prefix, "\\", "/")
	prefix = strings.TrimLeft(prefix, "/")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return "", pathutil.ErrInvalidPath
	}
	clean := path.Clean(prefix)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") {
		return "", pathutil.ErrInvalidPath
	}
	return clean, nil
}
