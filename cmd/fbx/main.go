package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pixfid/go-fbx/fbx"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "convert-zip":
		os.Exit(runConvertZip(os.Args[2:]))
	case "interactive":
		os.Exit(runInteractive(os.Args[2:]))
	case "pack":
		os.Exit(runPack(os.Args[2:]))
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
  fbx interactive [--max-entry-size bytes] [--max-chunk-size bytes] [container.fbx]
  fbx pack [--codec store|zstd|lz4] [--level n] [--chunk-text n] [--chunk-bin n] [--workers n] [--verify-in] [--max-entry-size bytes] [--max-chunk-size bytes] <input.fbx> [-o output.fbx]
  fbx add [--as entry/path] [--meta-json json] [--meta-file file.json] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx upsert [--as entry/path] [--meta-json json] [--meta-file file.json] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx replace [--as entry/path] [--meta-json json] [--meta-file file.json] [--keep-meta] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx rm [--prefix p] [--glob g] [--contains s] [--min-size n] [--max-size n] <container.fbx> [entry ...]
  fbx find [--prefix p] [--glob g] [--contains s] <container.fbx>
  fbx stat [--json] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <entry-path>
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

type interactiveSession struct {
	c             *fbx.Container
	containerPath string
	cwd           string
	opts          *fbx.Options
	out           io.Writer
	errOut        io.Writer
	lastViewPath  string
	lastViewOff   uint64
	lastViewSize  int
	lastViewShown int
}

func runInteractive(args []string) int {
	fs := flag.NewFlagSet("interactive", flag.ContinueOnError)
	maxEntrySize := fs.Uint64("max-entry-size", 0, "maximum entry size in bytes (0 = unlimited)")
	maxChunkSize := fs.Uint64("max-chunk-size", 0, "maximum chunk size in bytes (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "interactive accepts at most one optional <container.fbx>")
		return 2
	}
	copts, err := buildLimitOptions(*maxEntrySize, *maxChunkSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	s := &interactiveSession{
		opts:         copts,
		out:          os.Stdout,
		errOut:       os.Stderr,
		lastViewSize: 1024,
	}
	if fs.NArg() == 1 {
		if err := s.openContainer(fs.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	fmt.Fprintln(os.Stderr, "interactive mode; type 'help' for commands")
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for {
		fmt.Fprint(os.Stderr, s.prompt())
		if !sc.Scan() {
			fmt.Fprintln(os.Stderr)
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		exit, code := s.runCommand(line)
		if code != 0 {
			continue
		}
		if exit {
			break
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		s.closeContainer()
		return 1
	}
	s.closeContainer()
	return 0
}

func (s *interactiveSession) prompt() string {
	if s.c == nil {
		return "fbx> "
	}
	base := filepath.Base(s.containerPath)
	if s.cwd == "" {
		return fmt.Sprintf("fbx[%s:/]> ", base)
	}
	return fmt.Sprintf("fbx[%s:/%s]> ", base, s.cwd)
}

func (s *interactiveSession) runCommand(line string) (bool, int) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, 0
	}
	cmd := strings.ToLower(parts[0])
	switch cmd {
	case "help", "?":
		fmt.Fprintln(s.out, "commands: help, open <fbx>, close, pwd, cd <path>, ls [path], stat <entry>, cat <entry> [offset] [size], next, prev, rm <entry>, exit")
		return false, 0
	case "exit", "quit":
		return true, 0
	case "open":
		if len(parts) != 2 {
			fmt.Fprintln(s.errOut, "open requires <container.fbx>")
			return false, 2
		}
		if err := s.openContainer(parts[1]); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		fmt.Fprintf(s.out, "opened %s\n", s.containerPath)
		return false, 0
	case "close":
		s.closeContainer()
		fmt.Fprintln(s.out, "closed")
		return false, 0
	case "pwd":
		if s.cwd == "" {
			fmt.Fprintln(s.out, "/")
			return false, 0
		}
		fmt.Fprintf(s.out, "/%s\n", s.cwd)
		return false, 0
	case "cd":
		if len(parts) != 2 {
			fmt.Fprintln(s.errOut, "cd requires <path>")
			return false, 2
		}
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		next, err := s.resolveCD(parts[1])
		if err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		if !s.prefixExists(next) {
			fmt.Fprintln(s.errOut, "path not found")
			return false, 1
		}
		s.cwd = next
		return false, 0
	case "ls":
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		target := s.cwd
		if len(parts) >= 2 {
			var err error
			target, err = s.resolveCD(parts[1])
			if err != nil {
				fmt.Fprintln(s.errOut, err)
				return false, 1
			}
		}
		if err := s.listPrefix(target); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		return false, 0
	case "stat":
		if len(parts) != 2 {
			fmt.Fprintln(s.errOut, "stat requires <entry>")
			return false, 2
		}
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		p, err := s.resolveEntry(parts[1])
		if err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		info, err := s.c.Stat(p)
		if err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		fmt.Fprintf(s.out, "path=%s\nsize=%d\nmtime_unix=%d\nmode=%#o\nflags=%d\nmeta_size=%d\n", info.Path, info.Size, info.MTimeUnix, info.Mode, info.Flags, len(info.Meta))
		if len(info.Meta) > 0 {
			if json.Valid(info.Meta) {
				fmt.Fprintf(s.out, "meta_json=%s\n", string(info.Meta))
			} else if utf8.Valid(info.Meta) {
				fmt.Fprintf(s.out, "meta_text=%s\n", string(info.Meta))
			} else {
				fmt.Fprintf(s.out, "meta_hex=%s\n", hex.EncodeToString(info.Meta))
			}
		}
		return false, 0
	case "cat":
		if len(parts) < 2 || len(parts) > 4 {
			fmt.Fprintln(s.errOut, "cat requires <entry> [offset] [size]")
			return false, 2
		}
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		p, err := s.resolveEntry(parts[1])
		if err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		off := uint64(0)
		size := s.lastViewSize
		if len(parts) >= 3 {
			v, err := strconv.ParseUint(parts[2], 10, 64)
			if err != nil {
				fmt.Fprintln(s.errOut, "offset must be uint")
				return false, 2
			}
			off = v
		}
		if len(parts) == 4 {
			v, err := strconv.Atoi(parts[3])
			if err != nil || v <= 0 {
				fmt.Fprintln(s.errOut, "size must be > 0")
				return false, 2
			}
			size = v
		}
		if err := s.viewChunk(p, off, size); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		return false, 0
	case "next":
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		if s.lastViewPath == "" {
			fmt.Fprintln(s.errOut, "nothing to continue; use cat first")
			return false, 2
		}
		off := s.lastViewOff + uint64(s.lastViewShown)
		if err := s.viewChunk(s.lastViewPath, off, s.lastViewSize); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		return false, 0
	case "prev":
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		if s.lastViewPath == "" {
			fmt.Fprintln(s.errOut, "nothing to continue; use cat first")
			return false, 2
		}
		var off uint64
		if s.lastViewOff > uint64(s.lastViewSize) {
			off = s.lastViewOff - uint64(s.lastViewSize)
		}
		if err := s.viewChunk(s.lastViewPath, off, s.lastViewSize); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		return false, 0
	case "rm":
		if len(parts) != 2 {
			fmt.Fprintln(s.errOut, "rm requires <entry>")
			return false, 2
		}
		if err := s.requireOpen(); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		p, err := s.resolveEntry(parts[1])
		if err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		if err := s.c.Remove(p); err != nil {
			fmt.Fprintln(s.errOut, err)
			return false, 1
		}
		if s.lastViewPath == p {
			s.lastViewPath = ""
			s.lastViewOff = 0
			s.lastViewShown = 0
		}
		fmt.Fprintf(s.out, "removed %s\n", p)
		return false, 0
	default:
		fmt.Fprintln(s.errOut, "unknown command; use 'help'")
		return false, 2
	}
}

func (s *interactiveSession) openContainer(p string) error {
	c, err := fbx.Open(p, s.opts)
	if err != nil {
		return err
	}
	if s.c != nil {
		_ = s.c.Close()
	}
	s.c = c
	s.containerPath = p
	s.cwd = ""
	s.lastViewPath = ""
	s.lastViewOff = 0
	s.lastViewShown = 0
	return nil
}

func (s *interactiveSession) closeContainer() {
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
	s.containerPath = ""
	s.cwd = ""
	s.lastViewPath = ""
	s.lastViewOff = 0
	s.lastViewShown = 0
}

func (s *interactiveSession) requireOpen() error {
	if s.c == nil {
		return fmt.Errorf("no container is open; use open <container.fbx>")
	}
	return nil
}

func (s *interactiveSession) resolveCD(arg string) (string, error) {
	arg = strings.TrimSpace(strings.ReplaceAll(arg, "\\", "/"))
	if arg == "" || arg == "." {
		return s.cwd, nil
	}
	if arg == "/" {
		return "", nil
	}
	base := s.cwd
	if strings.HasPrefix(arg, "/") {
		base = ""
		arg = strings.TrimLeft(arg, "/")
	}
	joined := path.Clean(path.Join("/", base, arg))
	if !strings.HasPrefix(joined, "/") {
		return "", fmt.Errorf("invalid path")
	}
	out := strings.TrimPrefix(joined, "/")
	if out == "." {
		return "", nil
	}
	if out == "" {
		return "", nil
	}
	for _, part := range strings.Split(out, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid path")
		}
	}
	return out, nil
}

func (s *interactiveSession) resolveEntry(arg string) (string, error) {
	p, err := s.resolveCD(arg)
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", fmt.Errorf("entry path is required")
	}
	return p, nil
}

func (s *interactiveSession) prefixExists(prefix string) bool {
	if prefix == "" {
		return true
	}
	if _, err := s.c.Stat(prefix); err == nil {
		return true
	}
	it := s.c.List()
	want := prefix + "/"
	for it.Next() {
		if strings.HasPrefix(it.Value().Path, want) {
			return true
		}
	}
	return false
}

func (s *interactiveSession) listPrefix(prefix string) error {
	dirs := map[string]struct{}{}
	files := make([]fbx.EntryInfo, 0)
	var targetFile *fbx.EntryInfo
	if prefix != "" {
		if info, err := s.c.Stat(prefix); err == nil {
			cp := info
			targetFile = &cp
		}
	}
	it := s.c.List()
	fullPrefix := prefix
	if fullPrefix != "" {
		fullPrefix += "/"
	}
	for it.Next() {
		e := it.Value()
		rel := e.Path
		if prefix != "" {
			if e.Path == prefix {
				continue
			}
			if !strings.HasPrefix(e.Path, fullPrefix) {
				continue
			}
			rel = strings.TrimPrefix(e.Path, fullPrefix)
		}
		if rel == "" {
			continue
		}
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			dirs[rel[:i]] = struct{}{}
			continue
		}
		e.Path = rel
		files = append(files, e)
	}
	if err := it.Err(); err != nil {
		return err
	}
	if targetFile != nil && len(dirs) == 0 && len(files) == 0 {
		fmt.Fprintf(s.out, "%12d  %s\n", targetFile.Size, path.Base(targetFile.Path))
		return nil
	}
	dirNames := make([]string, 0, len(dirs))
	for d := range dirs {
		dirNames = append(dirNames, d)
	}
	sort.Strings(dirNames)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(dirNames) == 0 && len(files) == 0 {
		fmt.Fprintln(s.out, "(empty)")
		return nil
	}
	for _, d := range dirNames {
		fmt.Fprintf(s.out, "%12s  %s/\n", "-", d)
	}
	for _, f := range files {
		fmt.Fprintf(s.out, "%12d  %s\n", f.Size, f.Path)
	}
	return nil
}

func (s *interactiveSession) viewChunk(entryPath string, off uint64, size int) error {
	info, err := s.c.Stat(entryPath)
	if err != nil {
		return err
	}
	if off > info.Size {
		return fmt.Errorf("offset out of range")
	}
	r, err := s.c.OpenReader(entryPath)
	if err != nil {
		return err
	}
	defer r.Close()
	if off > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(off)); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	buf := make([]byte, size)
	n, err := io.ReadFull(r, buf)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	if err != nil {
		return err
	}
	buf = buf[:n]
	fmt.Fprintf(s.out, "[%s] offset=%d shown=%d total=%d\n", entryPath, off, len(buf), info.Size)
	if len(buf) == 0 {
		fmt.Fprintln(s.out, "(eof)")
	} else if utf8.Valid(buf) && bytes.IndexByte(buf, 0) < 0 {
		fmt.Fprintln(s.out, string(buf))
	} else {
		fmt.Fprint(s.out, hex.Dump(buf))
	}
	s.lastViewPath = entryPath
	s.lastViewOff = off
	s.lastViewSize = size
	s.lastViewShown = len(buf)
	return nil
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

func runPack(args []string) int {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default: in-place rewrite)")
	codec := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkText := fs.Int("chunk-text", 0, "text chunk size in bytes")
	chunkBin := fs.Int("chunk-bin", 0, "binary chunk size in bytes")
	workers := fs.Int("workers", 0, "parallel workers for chunk compression")
	verifyIn := fs.Bool("verify-in", true, "verify input container before pack")
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
		MaxEntrySize: *maxEntrySize,
	}
	if *maxChunkSize > uint64(^uint32(0)) {
		fmt.Fprintf(os.Stderr, "--max-chunk-size must be <= %d\n", uint64(^uint32(0)))
		return 2
	}
	popts.MaxChunkSize = uint32(*maxChunkSize)
	switch *codec {
	case "store":
		popts.Codec = fbx.CodecStore
	case "zstd":
		popts.Codec = fbx.CodecZstd
	case "lz4":
		popts.Codec = fbx.CodecLZ4
	default:
		fmt.Fprintln(os.Stderr, "--codec must be store|zstd|lz4")
		return 2
	}
	if err := fbx.Pack(input, output, popts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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
