package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pixfid/go-fbx/fbx"
	"github.com/pixfid/go-fbx/internal/format"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "convert-zip":
		os.Exit(runConvertZip(os.Args[2:]))
	case "pack":
		os.Exit(runPack(os.Args[2:]))
	case "pack-many":
		os.Exit(runPackMany(os.Args[2:]))
	case "add":
		os.Exit(runAdd(os.Args[2:]))
	case "upsert":
		os.Exit(runUpsert(os.Args[2:]))
	case "replace":
		os.Exit(runReplace(os.Args[2:]))
	case "rm":
		os.Exit(runRm(os.Args[2:]))
	case "find":
		os.Exit(runFind(os.Args[2:]))
	case "stat":
		os.Exit(runStat(os.Args[2:]))
	case "set-meta":
		os.Exit(runSetMeta(os.Args[2:]))
	case "info":
		os.Exit(runInfo(os.Args[2:]))
	case "replace-text":
		os.Exit(runReplaceText(os.Args[2:]))
	case "list":
		os.Exit(runList(os.Args[2:]))
	case "extract":
		os.Exit(runExtract(os.Args[2:]))
	case "verify":
		os.Exit(runVerify(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `fbx CLI

Usage:
  fbx convert-zip [--meta auto|none] [--meta-file file.json] [--prefix p] [--codec store|zstd|lz4] [--level n] [--progress] [--overwrite] [--max-entry-size bytes] [--max-chunk-size bytes] <input.zip> <output.fbx>
  fbx pack [--codec store|zstd|lz4] [--level n] [--chunk-text n] [--chunk-bin n] [--workers n] [--verify-in] [--fast] [--progress] [--max-entry-size bytes] [--max-chunk-size bytes] <input.fbx> [-o output.fbx]
  fbx pack-many [--jobs n] [--glob pattern] [--codec store|zstd|lz4] [--level n] [--chunk-text n] [--chunk-bin n] [--workers n] [--verify-in] [--fast] [--max-entry-size bytes] [--max-chunk-size bytes] <input1.fbx> [input2.fbx ...]
  fbx add [--as entry/path] [--meta-json json] [--meta-file file.json] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx upsert [--as entry/path] [--meta-json json] [--meta-file file.json] [--keep-meta] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx replace [--as entry/path] [--meta-json json] [--meta-file file.json] [--keep-meta] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx rm [--prefix p] [--glob g] [--contains s] [--min-size n] [--max-size n] <container.fbx> [entry ...]
  fbx find [--prefix p] [--glob g] [--contains s] <container.fbx>
  fbx stat [--json] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <entry-path>
  fbx info [--json] <container.fbx>
  fbx set-meta [--meta-json json|--meta-file file.json] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <entry-path>
  fbx replace-text --find old --replace new [--prefix p] [--glob g] [--dry-run] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx>
  fbx list <container.fbx>
  fbx extract [-o output] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <entry-path>
  fbx verify [--mode dir|sample|all] <container.fbx>
`)
}

func runConvertZip(args []string) int {
	fs := flag.NewFlagSet("convert-zip", flag.ContinueOnError)
	metaMode := fs.String("meta", "auto", "metadata mode: auto|none")
	metaFile := fs.String("meta-file", "", "JSON map path->metadata object")
	prefix := fs.String("prefix", "", "path prefix inside FBX")
	codec := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	showProgress := fs.Bool("progress", true, "show conversion progress")
	overwrite := fs.Bool("overwrite", false, "overwrite output file")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "convert-zip requires <input.zip> <output.fbx>")
		return 2
	}
	input := fs.Arg(0)
	output := fs.Arg(1)
	includeMeta := true
	if *metaMode == "none" {
		includeMeta = false
	} else if *metaMode != "auto" {
		fmt.Fprintln(os.Stderr, "--meta must be auto or none")
		return 2
	}
	wopts := &fbx.WriteOptions{Level: *level}
	switch *codec {
	case "store":
		wopts.Codec = fbx.CodecStore
	case "zstd":
		wopts.Codec = fbx.CodecZstd
	case "lz4":
		wopts.Codec = fbx.CodecLZ4
	default:
		fmt.Fprintln(os.Stderr, "--codec must be store|zstd|lz4")
		return 2
	}
	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var progress func(fbx.ZIPImportProgress)
	if *showProgress {
		pp := &zipProgressPrinter{lastRender: time.Time{}}
		progress = pp.Handle
	}
	err = fbx.ConvertZIPToFBX(input, output, &fbx.ZIPImportOptions{
		IncludeMetadata:  includeMeta,
		MetaFile:         *metaFile,
		PathPrefix:       *prefix,
		Overwrite:        *overwrite,
		ContainerOptions: copts,
		WriteOptions:     wopts,
		Progress:         progress,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type zipProgressPrinter struct {
	lastRender time.Time
	lastLine   string
}

func (p *zipProgressPrinter) Handle(ev fbx.ZIPImportProgress) {
	switch ev.Phase {
	case "start":
		p.render(0, ev.FilesTotal, false)
	case "file_start":
		p.render(ev.FilesDone+1, ev.FilesTotal, false)
	case "file_progress":
		now := time.Now()
		if !p.lastRender.IsZero() && now.Sub(p.lastRender) < 200*time.Millisecond {
			return
		}
		p.lastRender = now
		p.render(ev.FilesDone+1, ev.FilesTotal, false)
	case "file_done":
		p.render(ev.FilesDone, ev.FilesTotal, false)
	case "done":
		p.render(ev.FilesDone, ev.FilesTotal, true)
	}
}

func (p *zipProgressPrinter) HandlePack(ev fbx.PackProgress) {
	switch ev.Phase {
	case "start":
		p.render(0, ev.EntriesTotal, false)
	case "entry_start":
		p.render(ev.EntriesDone+1, ev.EntriesTotal, false)
	case "entry_done":
		p.render(ev.EntriesDone, ev.EntriesTotal, false)
	case "done":
		p.render(ev.EntriesDone, ev.EntriesTotal, true)
	}
}

func (p *zipProgressPrinter) render(done, total int, finish bool) {
	if total <= 0 {
		total = 1
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}

	const width = 100
	filled := done * width / total
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "▓"
		} else {
			bar += "░"
		}
	}
	line := fmt.Sprintf("[%d/%d] %s", done, total, bar)
	if line == p.lastLine && !finish {
		return
	}
	if finish {
		if line == p.lastLine {
			fmt.Fprintln(os.Stderr)
			return
		}
		p.lastLine = line
		fmt.Fprintf(os.Stderr, "\r\033[2K%s\n", line)
		return
	}
	p.lastLine = line
	fmt.Fprintf(os.Stderr, "\r\033[2K%s", line)
}

func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "list requires <container.fbx>")
		return 2
	}
	c, err := fbx.Open(fs.Arg(0), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	it := c.List()
	for it.Next() {
		e := it.Value()
		fmt.Printf("%12d  %s\n", e.Size, e.Path)
	}
	if err := it.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type codecReport struct {
	EntriesTotal int            `json:"entries_total"`
	ChunksTotal  int            `json:"chunks_total"`
	ChunkCounts  map[string]int `json:"chunk_counts"`
	Codec        string         `json:"codec"`
	Level        string         `json:"level"`
	LevelCounts  map[string]int `json:"level_counts"`
}

func runInfo(args []string) int {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "info requires <container.fbx>")
		return 2
	}
	report, err := inspectContainerCodecs(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("entries_total=%d\n", report.EntriesTotal)
	fmt.Printf("chunks_total=%d\n", report.ChunksTotal)
	fmt.Printf("codec=%s\n", report.Codec)
	fmt.Printf("level=%s\n", report.Level)
	keys := make([]string, 0, len(report.ChunkCounts))
	for k := range report.ChunkCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("chunks_%s=%d\n", k, report.ChunkCounts[k])
	}
	for _, k := range sortedLevelCountKeys(report.LevelCounts) {
		fmt.Printf("chunks_level_%s=%d\n", k, report.LevelCounts[k])
	}
	return 0
}

func sortedLevelCountKeys(m map[string]int) []string {
	if len(m) == 0 {
		return nil
	}
	intKeys := make([]int, 0, len(m))
	for k := range m {
		v, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		intKeys = append(intKeys, v)
	}
	sort.Ints(intKeys)
	out := make([]string, 0, len(intKeys))
	for _, k := range intKeys {
		out = append(out, strconv.Itoa(k))
	}
	return out
}

func inspectContainerCodecs(containerPath string) (codecReport, error) {
	f, err := os.Open(containerPath)
	if err != nil {
		return codecReport{}, err
	}
	defer f.Close()

	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		return codecReport{}, err
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		return codecReport{}, err
	}
	dirBlob := make([]byte, h.DirSize)
	if _, err := f.ReadAt(dirBlob, int64(h.DirOffset)); err != nil {
		return codecReport{}, err
	}
	dir, err := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize)
	if err != nil {
		return codecReport{}, err
	}

	counts := map[string]int{
		"store": 0,
		"zstd":  0,
		"lz4":   0,
	}
	levelCounts := map[string]int{}
	totalChunks := 0
	for _, e := range dir.Entries {
		for _, ref := range e.Chunks {
			codec, level, err := readChunkHeaderAt(f, int64(ref.ChunkOffset))
			if err != nil {
				return codecReport{}, err
			}
			switch codec {
			case format.CodecStore:
				counts["store"]++
			case format.CodecZstd:
				counts["zstd"]++
			case format.CodecLZ4:
				counts["lz4"]++
			default:
				counts[fmt.Sprintf("unknown_%d", codec)]++
			}
			levelCounts[strconv.Itoa(int(level))]++
			totalChunks++
		}
	}
	used := make([]string, 0, len(counts))
	for name, n := range counts {
		if n > 0 {
			used = append(used, name)
		}
	}
	sort.Strings(used)
	codecSummary := "none"
	if len(used) == 1 {
		codecSummary = used[0]
	} else if len(used) > 1 {
		codecSummary = "mixed(" + strings.Join(used, ",") + ")"
	}
	usedLevels := sortedLevelCountKeys(levelCounts)
	levelSummary := "none"
	if len(usedLevels) == 1 {
		levelSummary = usedLevels[0]
	} else if len(usedLevels) > 1 {
		levelSummary = "mixed(" + strings.Join(usedLevels, ",") + ")"
	}
	return codecReport{
		EntriesTotal: len(dir.Entries),
		ChunksTotal:  totalChunks,
		ChunkCounts:  counts,
		Codec:        codecSummary,
		Level:        levelSummary,
		LevelCounts:  levelCounts,
	}, nil
}

func inspectEntryCodecs(containerPath string) (map[string]string, error) {
	f, err := os.Open(containerPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		return nil, err
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		return nil, err
	}
	dirBlob := make([]byte, h.DirSize)
	if _, err := f.ReadAt(dirBlob, int64(h.DirOffset)); err != nil {
		return nil, err
	}
	dir, err := format.DecodeDirectory(dirBlob, h.DirCRC32, h.DirSize)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(dir.Entries))
	for _, e := range dir.Entries {
		counts := map[string]int{"store": 0, "zstd": 0, "lz4": 0}
		for _, ref := range e.Chunks {
			codec, err := readChunkCodecAt(f, int64(ref.ChunkOffset))
			if err != nil {
				return nil, err
			}
			switch codec {
			case format.CodecStore:
				counts["store"]++
			case format.CodecZstd:
				counts["zstd"]++
			case format.CodecLZ4:
				counts["lz4"]++
			default:
				counts[fmt.Sprintf("unknown_%d", codec)]++
			}
		}
		used := make([]string, 0, len(counts))
		for k, n := range counts {
			if n > 0 {
				used = append(used, k)
			}
		}
		sort.Strings(used)
		summary := "none"
		if len(used) == 1 {
			summary = used[0]
		} else if len(used) > 1 {
			summary = "mixed(" + strings.Join(used, ",") + ")"
		}
		out[e.Path] = summary
	}
	return out, nil
}

func readChunkCodecAt(r io.ReaderAt, off int64) (format.Codec, error) {
	codec, _, err := readChunkHeaderAt(r, off)
	return codec, err
}

func readChunkHeaderAt(r io.ReaderAt, off int64) (format.Codec, uint8, error) {
	var head [4]byte
	if _, err := r.ReadAt(head[:], off); err != nil {
		return 0, 0, err
	}
	if head[0] != format.MagicChunk[0] || head[1] != format.MagicChunk[1] {
		return 0, 0, fbx.ErrInvalidFormat
	}
	return format.Codec(head[2]), head[3], nil
}

func runPack(args []string) int {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default: in-place rewrite)")
	codecStr := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkText := fs.Int("chunk-text", 0, "text chunk size in bytes")
	chunkBin := fs.Int("chunk-bin", 0, "binary chunk size in bytes")
	workers := fs.Int("workers", 0, "parallel workers for chunk compression")
	verifyIn := fs.Bool("verify-in", true, "verify input container before pack")
	fastUnsafe := fs.Bool("fast", false, "faster unsafe mode: disable CRC read checks and fsync on output")
	showProgress := fs.Bool("progress", true, "show pack progress")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "pack requires <input.fbx>")
		return 2
	}
	input := fs.Arg(0)
	output := *out
	if output == "" {
		output = input
	}

	popts := &fbx.PackOptions{
		Level:        *level,
		ChunkText:    *chunkText,
		ChunkBin:     *chunkBin,
		Workers:      *workers,
		VerifyIn:     *verifyIn,
		FastUnsafe:   *fastUnsafe,
		MaxEntrySize: *maxEntrySize,
	}
	if *showProgress {
		p := &zipProgressPrinter{}
		popts.Progress = p.HandlePack
	}
	if *maxChunkSize > uint64(^uint32(0)) {
		fmt.Fprintf(os.Stderr, "--max-chunk-size must be <= %d\n", uint64(^uint32(0)))
		return 2
	}
	popts.MaxChunkSize = uint32(*maxChunkSize)
	codec, err := parseCodec(*codecStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	popts.Codec = codec
	if output == input && *chunkText == 0 && *chunkBin == 0 {
		matched, err := containerMatchesPackParams(input, popts.Codec, popts.Level)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if matched {
			fmt.Fprintf(os.Stderr, "pack: skip, already codec=%s level=%d\n", codecName(popts.Codec), popts.Level)
			return 0
		}
	}
	if err := fbx.Pack(input, output, popts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runPackMany(args []string) int {
	fs := flag.NewFlagSet("pack-many", flag.ContinueOnError)
	jobs := fs.Int("jobs", 0, "number of files to pack in parallel (default: GOMAXPROCS)")
	glob := fs.String("glob", "", "glob pattern for input containers")
	codecStr := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkText := fs.Int("chunk-text", 0, "text chunk size in bytes")
	chunkBin := fs.Int("chunk-bin", 0, "binary chunk size in bytes")
	workers := fs.Int("workers", 0, "parallel workers for chunk compression")
	verifyIn := fs.Bool("verify-in", true, "verify input container before pack")
	fastUnsafe := fs.Bool("fast", false, "faster unsafe mode: disable CRC read checks and fsync on output")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 && *glob == "" {
		fmt.Fprintln(os.Stderr, "pack-many requires at least one input file or --glob pattern")
		return 2
	}
	if *maxChunkSize > uint64(^uint32(0)) {
		fmt.Fprintf(os.Stderr, "--max-chunk-size must be <= %d\n", uint64(^uint32(0)))
		return 2
	}
	codec, err := parseCodec(*codecStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	popts := fbx.PackOptions{
		Codec:        codec,
		Level:        *level,
		ChunkText:    *chunkText,
		ChunkBin:     *chunkBin,
		Workers:      *workers,
		VerifyIn:     *verifyIn,
		FastUnsafe:   *fastUnsafe,
		MaxEntrySize: *maxEntrySize,
		MaxChunkSize: uint32(*maxChunkSize),
	}

	inputs := make([]string, 0, fs.NArg()+32)
	inputs = append(inputs, fs.Args()...)
	if *glob != "" {
		matches, err := filepath.Glob(*glob)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		inputs = append(inputs, matches...)
	}
	inputs = uniqueSortedStrings(inputs)
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "pack-many: no input files matched")
		return 2
	}

	parallel := *jobs
	if parallel <= 0 {
		parallel = runtime.GOMAXPROCS(0)
	}
	if parallel > len(inputs) {
		parallel = len(inputs)
	}
	if parallel <= 0 {
		parallel = 1
	}

	type packManyResult struct {
		path    string
		err     error
		skipped bool
	}
	workCh := make(chan string, len(inputs))
	resCh := make(chan packManyResult, len(inputs))
	for _, p := range inputs {
		workCh <- p
	}
	close(workCh)

	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range workCh {
				if popts.ChunkText == 0 && popts.ChunkBin == 0 {
					matched, err := containerMatchesPackParams(p, popts.Codec, popts.Level)
					if err != nil {
						resCh <- packManyResult{path: p, err: err}
						continue
					}
					if matched {
						resCh <- packManyResult{path: p, skipped: true}
						continue
					}
				}
				opts := popts
				err := fbx.Pack(p, p, &opts)
				resCh <- packManyResult{path: p, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(resCh)
	}()

	done := 0
	failed := 0
	for res := range resCh {
		done++
		if res.skipped {
			fmt.Printf("[%d/%d] SKIP %s (already codec=%s level=%d)\n", done, len(inputs), res.path, codecName(popts.Codec), popts.Level)
			continue
		}
		if res.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s: %v\n", done, len(inputs), res.path, res.err)
			continue
		}
		fmt.Printf("[%d/%d] OK %s\n", done, len(inputs), res.path)
	}
	fmt.Printf("pack-many: total=%d success=%d failed=%d\n", len(inputs), len(inputs)-failed, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

func uniqueSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func codecName(c fbx.Codec) string {
	switch c {
	case fbx.CodecStore:
		return "store"
	case fbx.CodecZstd:
		return "zstd"
	case fbx.CodecLZ4:
		return "lz4"
	default:
		return fmt.Sprintf("unknown_%d", c)
	}
}

func containerMatchesPackParams(containerPath string, codec fbx.Codec, level int) (bool, error) {
	report, err := inspectContainerCodecs(containerPath)
	if err != nil {
		return false, err
	}
	total := report.ChunksTotal
	codecKey := codecName(codec)
	if report.ChunkCounts[codecKey] != total {
		return false, nil
	}
	if report.LevelCounts[strconv.Itoa(level)] != total {
		return false, nil
	}
	return true, nil
}

func runAdd(args []string) int {
	return runAddLike(false, args)
}

func runUpsert(args []string) int {
	return runAddLike(true, args)
}

func runReplace(args []string) int {
	fs := flag.NewFlagSet("replace", flag.ContinueOnError)
	as := fs.String("as", "", "entry path inside FBX")
	metaJSON := fs.String("meta-json", "", "metadata JSON string")
	metaFile := fs.String("meta-file", "", "metadata JSON file")
	keepMeta := fs.Bool("keep-meta", true, "preserve existing metadata when --meta-* is not provided")
	codecStr := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkSize := fs.Int("chunk-size", 0, "chunk size in bytes")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "replace requires <container.fbx> <source-file>")
		return 2
	}
	containerPath := fs.Arg(0)
	sourcePath := fs.Arg(1)

	codec, err := parseCodec(*codecStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	meta, err := loadMeta(*metaJSON, *metaFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	src, err := os.Open(sourcePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c, err := fbx.Open(containerPath, copts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	entryPath := *as
	if entryPath == "" {
		entryPath = filepath.Base(sourcePath)
	}
	entryPath = strings.ReplaceAll(entryPath, "\\", "/")
	entryPath = strings.TrimLeft(entryPath, "/")

	if len(meta) == 0 && *keepMeta {
		info, err := c.Stat(entryPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		meta = info.Meta
	}
	wopts := &fbx.WriteOptions{
		Codec:     codec,
		Level:     *level,
		ChunkSize: *chunkSize,
		MTimeUnix: uint64(st.ModTime().Unix()),
		Mode:      uint32(st.Mode().Perm()),
	}
	if err := c.Replace(entryPath, src, meta, wopts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runAddLike(isUpsert bool, args []string) int {
	name := "add"
	if isUpsert {
		name = "upsert"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	as := fs.String("as", "", "entry path inside FBX")
	metaJSON := fs.String("meta-json", "", "metadata JSON string")
	metaFile := fs.String("meta-file", "", "metadata JSON file")
	keepMeta := fs.Bool("keep-meta", true, "for upsert: preserve existing metadata when replacing and --meta-* is not provided")
	codecStr := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkSize := fs.Int("chunk-size", 0, "chunk size in bytes")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "%s requires <container.fbx> <source-file>\n", name)
		return 2
	}
	containerPath := fs.Arg(0)
	sourcePath := fs.Arg(1)

	codec, err := parseCodec(*codecStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	meta, err := loadMeta(*metaJSON, *metaFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	src, err := os.Open(sourcePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c, err := fbx.Open(containerPath, copts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	entryPath := *as
	if entryPath == "" {
		entryPath = filepath.Base(sourcePath)
	}
	entryPath = strings.ReplaceAll(entryPath, "\\", "/")
	entryPath = strings.TrimLeft(entryPath, "/")

	if isUpsert && len(meta) == 0 && *keepMeta {
		if info, err := c.Stat(entryPath); err == nil {
			meta = info.Meta
		} else if err != fbx.ErrNotFound {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	wopts := &fbx.WriteOptions{
		Codec:     codec,
		Level:     *level,
		ChunkSize: *chunkSize,
		MTimeUnix: uint64(st.ModTime().Unix()),
		Mode:      uint32(st.Mode().Perm()),
	}
	if isUpsert {
		err = c.Upsert(entryPath, src, meta, wopts)
	} else {
		err = c.Add(entryPath, src, meta, wopts)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runRm(args []string) int {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	prefix := fs.String("prefix", "", "remove by prefix")
	glob := fs.String("glob", "", "remove by glob pattern")
	contains := fs.String("contains", "", "remove entries where path contains substring")
	minSize := fs.Uint64("min-size", 0, "remove entries with size >= this value")
	maxSize := fs.Uint64("max-size", 0, "remove entries with size <= this value")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "rm requires <container.fbx> [entry ...]")
		return 2
	}
	containerPath := fs.Arg(0)
	paths := fs.Args()[1:]
	if *prefix == "" && *glob == "" && *contains == "" && *minSize == 0 && *maxSize == 0 && len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "rm: specify paths and/or filters (--prefix/--glob/--contains/--min-size/--max-size)")
		return 2
	}

	c, err := fbx.Open(containerPath, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	tx, err := c.Begin()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	removed := 0
	if *prefix != "" {
		n, err := tx.RemovePrefix(*prefix)
		if err != nil {
			tx.Rollback()
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		removed += n
	}
	if *glob != "" {
		n, err := tx.RemoveGlob(*glob)
		if err != nil {
			tx.Rollback()
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		removed += n
	}
	if len(paths) > 0 {
		n, err := tx.RemoveMany(paths)
		if err != nil {
			tx.Rollback()
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		removed += n
	}
	if *contains != "" || *minSize > 0 || *maxSize > 0 {
		n, err := tx.RemoveWhere(func(e fbx.EntryInfo) bool {
			if *contains != "" && !strings.Contains(e.Path, *contains) {
				return false
			}
			if *minSize > 0 && e.Size < *minSize {
				return false
			}
			if *maxSize > 0 && e.Size > *maxSize {
				return false
			}
			return true
		})
		if err != nil {
			tx.Rollback()
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		removed += n
	}
	if removed == 0 {
		tx.Rollback()
		fmt.Println(0)
		return 0
	}
	if err := tx.Commit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(removed)
	return 0
}

func runFind(args []string) int {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	prefix := fs.String("prefix", "", "match prefix")
	glob := fs.String("glob", "", "glob pattern")
	contains := fs.String("contains", "", "substring match")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "find requires <container.fbx>")
		return 2
	}
	if *glob != "" {
		if _, err := path.Match(*glob, "x"); err != nil {
			fmt.Fprintln(os.Stderr, "invalid --glob pattern")
			return 2
		}
	}

	c, err := fbx.Open(fs.Arg(0), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	it := c.List()
	for it.Next() {
		e := it.Value()
		if *prefix != "" && !strings.HasPrefix(e.Path, *prefix) {
			continue
		}
		if *contains != "" && !strings.Contains(e.Path, *contains) {
			continue
		}
		if *glob != "" {
			ok, _ := path.Match(*glob, e.Path)
			if !ok {
				continue
			}
		}
		fmt.Println(e.Path)
	}
	if err := it.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runStat(args []string) int {
	fs := flag.NewFlagSet("stat", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output as JSON")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "stat requires <container.fbx> <entry-path>")
		return 2
	}

	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c, err := fbx.Open(fs.Arg(0), copts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	info, err := c.Stat(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *asJSON {
		out := map[string]any{
			"path":       info.Path,
			"size":       info.Size,
			"mtime_unix": info.MTimeUnix,
			"mode":       info.Mode,
			"flags":      info.Flags,
			"meta_size":  len(info.Meta),
		}
		if json.Valid(info.Meta) {
			out["meta"] = json.RawMessage(info.Meta)
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("path=%s\nsize=%d\nmtime_unix=%d\nmode=%#o\nflags=%d\nmeta_size=%d\n", info.Path, info.Size, info.MTimeUnix, info.Mode, info.Flags, len(info.Meta))
	return 0
}

func runSetMeta(args []string) int {
	fs := flag.NewFlagSet("set-meta", flag.ContinueOnError)
	metaJSON := fs.String("meta-json", "", "metadata JSON string")
	metaFile := fs.String("meta-file", "", "metadata JSON file")
	codecStr := fs.String("codec", "store", "chunk codec for rewritten entry: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkSize := fs.Int("chunk-size", 0, "chunk size in bytes")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "set-meta requires <container.fbx> <entry-path>")
		return 2
	}
	meta, err := loadMeta(*metaJSON, *metaFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(meta) == 0 {
		fmt.Fprintln(os.Stderr, "set-meta requires --meta-json or --meta-file")
		return 2
	}
	codec, err := parseCodec(*codecStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c, err := fbx.Open(fs.Arg(0), copts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	entryPath := fs.Arg(1)
	info, err := c.Stat(entryPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r, err := c.OpenReader(entryPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer r.Close()
	wopts := &fbx.WriteOptions{
		Codec:     codec,
		Level:     *level,
		ChunkSize: *chunkSize,
		MTimeUnix: info.MTimeUnix,
		Mode:      info.Mode,
		Flags:     info.Flags,
	}
	if err := c.Upsert(entryPath, r, meta, wopts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runReplaceText(args []string) int {
	fs := flag.NewFlagSet("replace-text", flag.ContinueOnError)
	find := fs.String("find", "", "text to find")
	replace := fs.String("replace", "", "replacement text")
	prefix := fs.String("prefix", "", "limit to entry path prefix")
	glob := fs.String("glob", "", "limit by glob pattern")
	dryRun := fs.Bool("dry-run", false, "only report changes")
	codecStr := fs.String("codec", "store", "chunk codec for rewritten entries: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkSize := fs.Int("chunk-size", 0, "chunk size in bytes")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "replace-text requires <container.fbx>")
		return 2
	}
	if *find == "" {
		fmt.Fprintln(os.Stderr, "--find must be non-empty")
		return 2
	}
	if *glob != "" {
		if _, err := path.Match(*glob, "x"); err != nil {
			fmt.Fprintln(os.Stderr, "invalid --glob pattern")
			return 2
		}
	}
	codec, err := parseCodec(*codecStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	c, err := fbx.Open(fs.Arg(0), copts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	var tx *fbx.Tx
	if !*dryRun {
		tx, err = c.Begin()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	oldB := []byte(*find)
	newB := []byte(*replace)
	entriesChanged := 0
	replacements := 0

	it := c.List()
	for it.Next() {
		e := it.Value()
		if *prefix != "" && !strings.HasPrefix(e.Path, *prefix) {
			continue
		}
		if *glob != "" {
			ok, _ := path.Match(*glob, e.Path)
			if !ok {
				continue
			}
		}

		var body bytes.Buffer
		if err := c.Extract(e.Path, &body); err != nil {
			if tx != nil {
				tx.Rollback()
			}
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		src := body.Bytes()
		n := bytes.Count(src, oldB)
		if n == 0 {
			continue
		}
		entriesChanged++
		replacements += n
		if *dryRun {
			continue
		}
		dst := bytes.ReplaceAll(src, oldB, newB)
		wopts := &fbx.WriteOptions{
			Codec:     codec,
			Level:     *level,
			ChunkSize: *chunkSize,
			MTimeUnix: e.MTimeUnix,
			Mode:      e.Mode,
			Flags:     e.Flags,
		}
		if err := tx.Upsert(e.Path, bytes.NewReader(dst), e.Meta, wopts); err != nil {
			tx.Rollback()
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if err := it.Err(); err != nil {
		if tx != nil {
			tx.Rollback()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if tx != nil {
		if entriesChanged == 0 {
			tx.Rollback()
		} else if err := tx.Commit(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Printf("entries_changed=%d replacements=%d\n", entriesChanged, replacements)
	return 0
}

func buildLimitOptions(maxEntrySize, maxChunkSize uint64) (*fbx.Options, error) {
	if maxChunkSize > uint64(^uint32(0)) {
		return nil, fmt.Errorf("--max-chunk-size must be <= %d", uint64(^uint32(0)))
	}
	if maxEntrySize == 0 && maxChunkSize == 0 {
		return nil, nil
	}
	return &fbx.Options{
		DetectText:               true,
		StoreIfAlreadyCompressed: true,
		SyncOnCommit:             true,
		StrictVerify:             true,
		MaxEntrySize:             maxEntrySize,
		MaxChunkSize:             uint32(maxChunkSize),
	}, nil
}

func parseCodec(v string) (fbx.Codec, error) {
	switch v {
	case "store":
		return fbx.CodecStore, nil
	case "zstd":
		return fbx.CodecZstd, nil
	case "lz4":
		return fbx.CodecLZ4, nil
	default:
		return 0, fmt.Errorf("--codec must be store|zstd|lz4")
	}
}

func loadMeta(metaJSON, metaFile string) ([]byte, error) {
	if metaJSON != "" && metaFile != "" {
		return nil, fmt.Errorf("use only one of --meta-json or --meta-file")
	}
	if metaJSON != "" {
		if !json.Valid([]byte(metaJSON)) {
			return nil, fmt.Errorf("--meta-json must be valid JSON")
		}
		return []byte(metaJSON), nil
	}
	if metaFile != "" {
		b, err := os.ReadFile(metaFile)
		if err != nil {
			return nil, err
		}
		if !json.Valid(b) {
			return nil, fmt.Errorf("--meta-file must contain valid JSON")
		}
		return b, nil
	}
	return nil, nil
}

func runExtract(args []string) int {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	out := fs.String("o", "", "output path (default stdout)")
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "extract requires <container.fbx> <entry-path>")
		return 2
	}
	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c, err := fbx.Open(fs.Arg(0), copts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	if *out == "" {
		if err := c.Extract(fs.Arg(1), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	if err := c.Extract(fs.Arg(1), f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	mode := fs.String("mode", "dir", "verification mode: dir|sample|all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "verify requires <container.fbx>")
		return 2
	}
	c, err := fbx.Open(fs.Arg(0), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()

	v := &fbx.VerifyOptions{Mode: fbx.VerifyDirectoryOnly}
	switch *mode {
	case "dir":
		v.Mode = fbx.VerifyDirectoryOnly
	case "sample":
		v.Mode = fbx.VerifySampledChunks
	case "all":
		v.Mode = fbx.VerifyAllChunks
	default:
		fmt.Fprintln(os.Stderr, "--mode must be dir|sample|all")
		return 2
	}
	report, err := c.Verify(v)
	if report != nil {
		fmt.Printf("entries_checked=%d chunks_checked=%d errors=%d\n", report.EntriesChecked, report.ChunksChecked, len(report.Errors))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
