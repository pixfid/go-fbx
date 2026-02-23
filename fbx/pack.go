package fbx

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Pack rebuilds a compact container with only live entries.
// If outPath equals inPath (or empty), packing is performed via a temporary file
// and then atomically swapped into place.
func Pack(inPath, outPath string, opts *PackOptions) error {
	if inPath == "" {
		return fmt.Errorf("fbx: input path is empty")
	}
	if outPath == "" {
		outPath = inPath
	}

	cfg := PackOptions{
		Codec:    CodecStore,
		VerifyIn: true,
	}
	if opts != nil {
		cfg = *opts
		if opts.Codec != CodecStore && opts.Codec != CodecZstd && opts.Codec != CodecLZ4 {
			cfg.Codec = CodecStore
		}
	}

	inAbs, err := filepath.Abs(inPath)
	if err != nil {
		return err
	}
	outAbs, err := filepath.Abs(outPath)
	if err != nil {
		return err
	}
	inPlace := inAbs == outAbs
	targetPath := outPath
	if inPlace {
		targetPath = inPath + fmt.Sprintf(".pack.tmp.%d", time.Now().UnixNano())
	}

	if err := packInto(inPath, targetPath, cfg); err != nil {
		_ = os.Remove(targetPath)
		return err
	}
	if !inPlace {
		return nil
	}

	if err := os.Rename(targetPath, inPath); err != nil {
		_ = os.Remove(targetPath)
		return err
	}
	return nil
}

func packInto(inPath, outPath string, opts PackOptions) (retErr error) {
	inOpts := &Options{
		StrictVerify: !opts.FastUnsafe,
		MaxEntrySize: opts.MaxEntrySize,
		MaxChunkSize: opts.MaxChunkSize,
	}
	src, err := Open(inPath, inOpts)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := src.Close(); retErr == nil && cerr != nil {
			retErr = cerr
		}
	}()

	if opts.VerifyIn {
		if _, err := src.Verify(&VerifyOptions{Mode: VerifyAllChunks}); err != nil {
			return err
		}
	}

	entries := make([]EntryInfo, 0, 1024)
	it := src.List()
	for it.Next() {
		entries = append(entries, it.Value())
	}
	if err := it.Err(); err != nil {
		return err
	}
	if opts.Progress != nil {
		opts.Progress(PackProgress{Phase: "start", EntriesTotal: len(entries)})
	}

	outCfg := defaultOptions()
	if opts.ChunkText > 0 {
		outCfg.ChunkSizeText = opts.ChunkText
	}
	if opts.ChunkBin > 0 {
		outCfg.ChunkSizeBin = opts.ChunkBin
	}
	if opts.Workers > 0 {
		outCfg.MaxWorkers = opts.Workers
	}
	if opts.MaxEntrySize > 0 {
		outCfg.MaxEntrySize = opts.MaxEntrySize
	}
	if opts.MaxChunkSize > 0 {
		outCfg.MaxChunkSize = opts.MaxChunkSize
	}
	if opts.FastUnsafe {
		outCfg.SyncOnCommit = false
	}
	dst, err := Create(outPath, &outCfg)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := dst.Close(); retErr == nil && cerr != nil {
			retErr = cerr
		}
	}()

	tx, err := dst.Begin()
	if err != nil {
		return err
	}

	for i, e := range entries {
		if opts.Progress != nil {
			opts.Progress(PackProgress{
				Phase:        "entry_start",
				EntriesDone:  i,
				EntriesTotal: len(entries),
				EntryPath:    e.Path,
			})
		}
		r, err := src.OpenReader(e.Path)
		if err != nil {
			tx.Rollback()
			return err
		}
		wopts := &WriteOptions{
			Codec:     opts.Codec,
			Level:     opts.Level,
			ChunkSize: pickChunkSize(e, opts),
			MTimeUnix: e.MTimeUnix,
			Mode:      e.Mode,
			Flags:     e.Flags,
		}
		err = tx.Upsert(e.Path, r, e.Meta, wopts)
		closeErr := r.Close()
		if err != nil {
			tx.Rollback()
			return err
		}
		if closeErr != nil {
			tx.Rollback()
			return closeErr
		}
		if opts.Progress != nil {
			opts.Progress(PackProgress{
				Phase:        "entry_done",
				EntriesDone:  i + 1,
				EntriesTotal: len(entries),
				EntryPath:    e.Path,
			})
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if opts.Progress != nil {
		opts.Progress(PackProgress{
			Phase:        "done",
			EntriesDone:  len(entries),
			EntriesTotal: len(entries),
		})
	}
	return nil
}

func pickChunkSize(e EntryInfo, opts PackOptions) int {
	if e.Flags&EntryFlagIsText != 0 && opts.ChunkText > 0 {
		return opts.ChunkText
	}
	if opts.ChunkBin > 0 {
		return opts.ChunkBin
	}
	return 0
}
