package main

import (
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

	tea "github.com/charmbracelet/bubbletea"
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
  fbx interactive [--max-entry-size bytes] [--max-chunk-size bytes] [container.fbx]
  fbx pack [--codec store|zstd|lz4] [--level n] [--chunk-text n] [--chunk-bin n] [--workers n] [--verify-in] [--progress] [--max-entry-size bytes] [--max-chunk-size bytes] <input.fbx> [-o output.fbx]
  fbx add [--as entry/path] [--meta-json json] [--meta-file file.json] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
  fbx upsert [--as entry/path] [--meta-json json] [--meta-file file.json] [--codec store|zstd|lz4] [--level n] [--chunk-size n] [--max-entry-size bytes] [--max-chunk-size bytes] <container.fbx> <source-file>
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
		out:          io.Discard,
		errOut:       io.Discard,
		lastViewSize: 1024,
	}
	model := newBrowserModel(s)
	if fs.NArg() == 1 {
		if err := s.openContainer(fs.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	model.reload()
	p := tea.NewProgram(model, tea.WithAltScreen())
	m, err := p.Run()
	s.closeContainer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if bm, ok := m.(browserModel); ok {
		if bm.exitCode != 0 {
			return bm.exitCode
		}
	}
	return 0
}

type browserModel struct {
	s             *interactiveSession
	width         int
	height        int
	focusPane     int // 0=list,1=meta,2=content
	entries       []fbx.EntryInfo
	selected      int
	listOffset    int
	contentOffset uint64
	contentLines  []string
	codecByPath   map[string]string
	status        string
	confirmDelete bool
	exitCode      int
}

func newBrowserModel(s *interactiveSession) browserModel {
	return browserModel{
		s:           s,
		width:       120,
		height:      36,
		focusPane:   0,
		codecByPath: map[string]string{},
		status:      "↑/↓ список, Tab смена окна, Backspace удалить, q выход",
	}
}

func (m browserModel) Init() tea.Cmd { return nil }

func (m browserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		if m.confirmDelete {
			switch msg.Type {
			case tea.KeyEsc:
				m.confirmDelete = false
				m.status = "Удаление отменено"
			case tea.KeyEnter:
				m.confirmDelete = false
				m.deleteSelected()
			case tea.KeyRunes:
				r := strings.ToLower(msg.String())
				if r == "y" || r == "д" {
					m.confirmDelete = false
					m.deleteSelected()
				} else if r == "n" || r == "т" {
					m.confirmDelete = false
					m.status = "Удаление отменено"
				}
			}
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			m.exitCode = 130
			return m, tea.Quit
		case tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyRunes:
			switch strings.ToLower(msg.String()) {
			case "q":
				return m, tea.Quit
			}
		case tea.KeyTab:
			m.focusPane = (m.focusPane + 1) % 3
		case tea.KeyShiftTab:
			m.focusPane = (m.focusPane + 2) % 3
		case tea.KeyUp:
			if m.focusPane == 0 {
				m.moveSelection(-1)
			} else if m.focusPane == 2 {
				m.scrollContent(-512)
			}
		case tea.KeyDown:
			if m.focusPane == 0 {
				m.moveSelection(1)
			} else if m.focusPane == 2 {
				m.scrollContent(512)
			}
		case tea.KeyPgUp:
			if m.focusPane == 0 {
				m.moveSelection(-10)
			} else if m.focusPane == 2 {
				m.scrollContent(-2048)
			}
		case tea.KeyPgDown:
			if m.focusPane == 0 {
				m.moveSelection(10)
			} else if m.focusPane == 2 {
				m.scrollContent(2048)
			}
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.entries) == 0 {
				m.status = "Нет записей для удаления"
				return m, nil
			}
			m.confirmDelete = true
			e := m.entries[m.selected]
			m.status = fmt.Sprintf("Удалить %s ? Enter/y - да, Esc/n - нет", e.Path)
		}
	}
	return m, nil
}

func (m browserModel) View() string {
	if m.width < 60 {
		return "Слишком узкий терминал для интерактивного режима.\nРасширьте окно.\n"
	}
	w := m.width
	h := m.height
	if h < 18 {
		h = 18
	}

	file := "(no file)"
	if m.s != nil && m.s.containerPath != "" {
		file = filepath.Base(m.s.containerPath)
	}
	header := "---" + file + strings.Repeat("-", max(3, w-3-len(file)))

	bodyH := h - 4
	if bodyH < 12 {
		bodyH = 12
	}
	leftW := w / 3
	if leftW < 22 {
		leftW = 22
	}
	if leftW > w-30 {
		leftW = w - 30
	}
	rightW := w - 3 - leftW
	metaH := bodyH / 3
	if metaH < 5 {
		metaH = 5
	}
	contentH := bodyH - metaH - 1

	listLines := m.renderList(leftW, bodyH)
	metaLines := m.renderMeta(rightW, metaH)
	contentLines := m.renderContent(rightW, contentH)

	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for row := 0; row < bodyH; row++ {
		var right string
		if row < metaH {
			right = metaLines[row]
		} else if row == metaH {
			right = strings.Repeat("─", rightW)
		} else {
			right = contentLines[row-metaH-1]
		}
		b.WriteString("│")
		b.WriteString(listLines[row])
		b.WriteString("│")
		b.WriteString(right)
		b.WriteString("│\n")
	}

	b.WriteString(strings.Repeat("-", w))
	b.WriteByte('\n')
	infoLines := m.renderDetails(w)
	for _, ln := range infoLines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat("-", w))
	return b.String()
}

func (m *browserModel) reload() {
	if m.s == nil || m.s.c == nil {
		m.entries = nil
		m.contentLines = []string{"container not open"}
		m.codecByPath = map[string]string{}
		if m.s == nil || m.s.containerPath == "" {
			m.status = "Откройте контейнер: fbx interactive <file.fbx>"
		}
		return
	}
	it := m.s.c.List()
	entries := make([]fbx.EntryInfo, 0, 1024)
	for it.Next() {
		entries = append(entries, it.Value())
	}
	m.entries = entries
	if m.selected >= len(m.entries) {
		m.selected = max(0, len(m.entries)-1)
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if cMap, err := inspectEntryCodecs(m.s.containerPath); err == nil {
		m.codecByPath = cMap
	} else {
		m.codecByPath = map[string]string{}
		m.status = "Не удалось прочитать кодеки: " + err.Error()
	}
	m.contentOffset = 0
	m.loadContent()
}

func (m *browserModel) moveSelection(delta int) {
	if len(m.entries) == 0 {
		return
	}
	n := m.selected + delta
	if n < 0 {
		n = 0
	}
	if n >= len(m.entries) {
		n = len(m.entries) - 1
	}
	if n != m.selected {
		m.selected = n
		m.contentOffset = 0
		m.loadContent()
	}
}

func (m *browserModel) scrollContent(delta int64) {
	if len(m.entries) == 0 {
		return
	}
	info := m.entries[m.selected]
	var off int64 = int64(m.contentOffset) + delta
	if off < 0 {
		off = 0
	}
	if uint64(off) > info.Size {
		off = int64(info.Size)
	}
	m.contentOffset = uint64(off)
	m.loadContent()
}

func (m *browserModel) loadContent() {
	if len(m.entries) == 0 || m.s == nil || m.s.c == nil {
		m.contentLines = []string{"(empty)"}
		return
	}
	e := m.entries[m.selected]
	data, total, err := readEntryWindow(m.s.c, e.Path, m.contentOffset, 8192)
	if err != nil {
		m.contentLines = []string{"error: " + err.Error()}
		return
	}
	lines := make([]string, 0, 32)
	lines = append(lines, fmt.Sprintf("offset %d / %d", m.contentOffset, total))
	if len(data) == 0 {
		lines = append(lines, "(eof)")
		m.contentLines = lines
		return
	}
	if utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 {
		lines = append(lines, splitOutputLines(string(data))...)
	} else {
		lines = append(lines, splitOutputLines(hex.Dump(data))...)
	}
	m.contentLines = lines
}

func (m *browserModel) deleteSelected() {
	if len(m.entries) == 0 || m.s == nil || m.s.c == nil {
		m.status = "Нет записи для удаления"
		return
	}
	p := m.entries[m.selected].Path
	if err := m.s.c.Remove(p); err != nil {
		m.status = "Ошибка удаления: " + err.Error()
		return
	}
	m.status = "Удалено: " + p
	m.reload()
}

func (m *browserModel) renderList(w, h int) []string {
	lines := make([]string, h)
	for i := range lines {
		lines[i] = clipPad("", w)
	}
	title := " entries "
	if m.focusPane == 0 {
		title = "▶ entries "
	}
	lines[0] = clipPad(title, w)
	visible := h - 1
	if visible < 1 {
		return lines
	}
	if len(m.entries) == 0 {
		lines[1] = clipPad("(empty)", w)
		return lines
	}
	if m.selected < m.listOffset {
		m.listOffset = m.selected
	}
	if m.selected >= m.listOffset+visible {
		m.listOffset = m.selected - visible + 1
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
	for i := 0; i < visible; i++ {
		idx := m.listOffset + i
		if idx >= len(m.entries) {
			break
		}
		p := path.Base(m.entries[idx].Path)
		prefix := "  "
		if idx == m.selected {
			prefix = "> "
		}
		lines[i+1] = clipPad(prefix+p, w)
	}
	return lines
}

func (m *browserModel) renderMeta(w, h int) []string {
	lines := make([]string, h)
	for i := range lines {
		lines[i] = clipPad("", w)
	}
	title := " meta "
	if m.focusPane == 1 {
		title = "▶ meta "
	}
	lines[0] = clipPad(title, w)
	if len(m.entries) == 0 {
		lines[1] = clipPad("(empty)", w)
		return lines
	}
	e := m.entries[m.selected]
	codec := m.codecByPath[e.Path]
	if codec == "" {
		codec = "unknown"
	}
	metaLabel := "meta: (empty)"
	if len(e.Meta) > 0 {
		if json.Valid(e.Meta) {
			metaLabel = "meta_json:"
		} else if utf8.Valid(e.Meta) {
			metaLabel = "meta_text:"
		} else {
			metaLabel = "meta_hex:"
		}
	}
	base := []string{
		"> " + path.Base(e.Path),
		"codec: " + codec,
		metaLabel,
	}
	if len(e.Meta) > 0 {
		if json.Valid(e.Meta) {
			var out bytes.Buffer
			if err := json.Indent(&out, e.Meta, "", "  "); err == nil {
				base = append(base, splitOutputLines(out.String())...)
			} else {
				base = append(base, string(e.Meta))
			}
		} else if utf8.Valid(e.Meta) {
			base = append(base, splitOutputLines(string(e.Meta))...)
		} else {
			base = append(base, splitOutputLines(hex.Dump(e.Meta))...)
		}
	}
	for i := 1; i < h && i-1 < len(base); i++ {
		lines[i] = clipPad(base[i-1], w)
	}
	return lines
}

func (m *browserModel) renderContent(w, h int) []string {
	lines := make([]string, h)
	for i := range lines {
		lines[i] = clipPad("", w)
	}
	title := " content "
	if m.focusPane == 2 {
		title = "▶ content "
	}
	lines[0] = clipPad(title, w)
	for i := 1; i < h && i-1 < len(m.contentLines); i++ {
		lines[i] = clipPad(m.contentLines[i-1], w)
	}
	return lines
}

func (m *browserModel) renderDetails(w int) []string {
	out := make([]string, 4)
	out[0] = clipPad("|                                           |", w)
	out[1] = clipPad("|             характеристики записи         |", w)
	if len(m.entries) == 0 {
		out[2] = clipPad("|                контейнер пуст             |", w)
		out[3] = clipPad("| "+m.status, w)
		return out
	}
	e := m.entries[m.selected]
	codec := m.codecByPath[e.Path]
	out[2] = clipPad(fmt.Sprintf("| path=%s size=%d codec=%s mode=%#o flags=%d mtime=%d meta=%dB", e.Path, e.Size, codec, e.Mode, e.Flags, e.MTimeUnix, len(e.Meta)), w)
	out[3] = clipPad("| "+m.status, w)
	return out
}

func readEntryWindow(c *fbx.Container, entryPath string, off uint64, maxN int) ([]byte, uint64, error) {
	info, err := c.Stat(entryPath)
	if err != nil {
		return nil, 0, err
	}
	if off > info.Size {
		return nil, info.Size, fmt.Errorf("offset out of range")
	}
	r, err := c.OpenReader(entryPath)
	if err != nil {
		return nil, info.Size, err
	}
	defer r.Close()
	if off > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(off)); err != nil && !errors.Is(err, io.EOF) {
			return nil, info.Size, err
		}
	}
	buf := make([]byte, maxN)
	n, err := io.ReadFull(r, buf)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	if err != nil {
		return nil, info.Size, err
	}
	return buf[:n], info.Size, nil
}

func clipPad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > width {
		if width >= 2 {
			r = append(r[:width-1], '…')
		} else {
			r = r[:width]
		}
	}
	if len(r) < width {
		r = append(r, []rune(strings.Repeat(" ", width-len(r)))...)
	}
	return string(r)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type interactiveModel struct {
	s        *interactiveSession
	lines    []string
	input    string
	history  []string
	histPos  int
	width    int
	height   int
	exitCode int
}

func newInteractiveModel(s *interactiveSession) interactiveModel {
	return interactiveModel{
		s:       s,
		lines:   make([]string, 0, 256),
		history: make([]string, 0, 128),
		histPos: -1,
		height:  24,
	}
}

func (m interactiveModel) Init() tea.Cmd {
	return nil
}

func (m interactiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.exitCode = 130
			return m, tea.Quit
		case tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			cmd := strings.TrimSpace(m.input)
			m.input = ""
			if cmd == "" {
				return m, nil
			}
			m.history = append(m.history, cmd)
			m.histPos = -1
			m.appendLine(m.s.prompt() + cmd)
			exit, code, outLines, errLines := m.execCommand(cmd)
			for _, ln := range outLines {
				m.appendLine(ln)
			}
			for _, ln := range errLines {
				m.appendLine("error: " + ln)
			}
			if code != 0 && len(errLines) == 0 {
				m.appendLine(fmt.Sprintf("error: command failed (%d)", code))
			}
			if exit {
				m.exitCode = 0
				return m, tea.Quit
			}
		case tea.KeyBackspace, tea.KeyCtrlH:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case tea.KeySpace:
			m.input += " "
		case tea.KeyCtrlU:
			m.input = ""
		case tea.KeyUp:
			if len(m.history) == 0 {
				return m, nil
			}
			if m.histPos == -1 {
				m.histPos = len(m.history) - 1
			} else if m.histPos > 0 {
				m.histPos--
			}
			m.input = m.history[m.histPos]
		case tea.KeyDown:
			if len(m.history) == 0 || m.histPos == -1 {
				return m, nil
			}
			if m.histPos < len(m.history)-1 {
				m.histPos++
				m.input = m.history[m.histPos]
			} else {
				m.histPos = -1
				m.input = ""
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.input += msg.String()
			}
		}
	}
	return m, nil
}

func (m interactiveModel) View() string {
	maxLines := m.height - 2
	if maxLines < 1 {
		maxLines = 1
	}
	start := 0
	if len(m.lines) > maxLines {
		start = len(m.lines) - maxLines
	}
	var b strings.Builder
	for _, ln := range m.lines[start:] {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	b.WriteString(m.s.prompt())
	b.WriteString(m.input)
	return b.String()
}

func (m *interactiveModel) appendLine(line string) {
	for _, ln := range splitOutputLines(line) {
		m.lines = append(m.lines, ln)
	}
	if len(m.lines) > 2000 {
		m.lines = m.lines[len(m.lines)-2000:]
	}
}

func (m *interactiveModel) execCommand(command string) (bool, int, []string, []string) {
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	prevOut, prevErr := m.s.out, m.s.errOut
	m.s.out = &outBuf
	m.s.errOut = &errBuf
	exit, code := m.s.runCommand(command)
	m.s.out = prevOut
	m.s.errOut = prevErr
	return exit, code, splitOutputLines(outBuf.String()), splitOutputLines(errBuf.String())
}

func splitOutputLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, ln := range raw {
		if ln == "" {
			continue
		}
		out = append(out, ln)
	}
	return out
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
	codec := fs.String("codec", "store", "chunk codec: store|zstd|lz4")
	level := fs.Int("level", 0, "codec compression level")
	chunkText := fs.Int("chunk-text", 0, "text chunk size in bytes")
	chunkBin := fs.Int("chunk-bin", 0, "binary chunk size in bytes")
	workers := fs.Int("workers", 0, "parallel workers for chunk compression")
	verifyIn := fs.Bool("verify-in", true, "verify input container before pack")
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
