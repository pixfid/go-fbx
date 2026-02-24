package fbx

import "runtime"

type Codec uint8

const (
	CodecStore Codec = 0
	CodecZstd  Codec = 1
	CodecLZ4   Codec = 2
)

type Options struct {
	ChunkSizeText            int
	ChunkSizeBin             int
	DetectText               bool
	DefaultCodec             Codec
	DefaultLevel             int
	StoreIfAlreadyCompressed bool
	MaxWorkers               int
	MaxEntrySize             uint64
	MaxChunkSize             uint32
	SyncOnCommit             bool
	StrictVerify             bool
}

func defaultOptions() Options {
	return Options{
		ChunkSizeText:            1 << 20,
		ChunkSizeBin:             4 << 20,
		DetectText:               true,
		DefaultCodec:             CodecStore,
		DefaultLevel:             0,
		StoreIfAlreadyCompressed: true,
		MaxWorkers:               runtime.GOMAXPROCS(0),
		SyncOnCommit:             true,
		StrictVerify:             true,
	}
}

type WriteOptions struct {
	Codec     Codec
	Level     int
	ChunkSize int
	MTimeUnix uint64
	Mode      uint32
	Flags     uint32
}

const (
	EntryFlagIsBinary uint32 = 1 << 0
	EntryFlagIsText   uint32 = 1 << 1
)

type VerifyMode int

const (
	VerifyDirectoryOnly VerifyMode = iota
	VerifySampledChunks
	VerifyAllChunks
)

type VerifyOptions struct {
	Mode VerifyMode
}

type VerifyReport struct {
	EntriesChecked uint64
	ChunksChecked  uint64
	Errors         []error
}

type PackOptions struct {
	Codec        Codec
	Level        int
	ChunkText    int
	ChunkBin     int
	Workers      int
	VerifyIn     bool
	FastUnsafe   bool
	ClearMeta    bool
	MaxEntrySize uint64
	MaxChunkSize uint32
	Progress     func(PackProgress)
}

type PackProgress struct {
	Phase        string
	EntriesDone  int
	EntriesTotal int
	EntryPath    string
}

type EntryInfo struct {
	Path      string
	Size      uint64
	MTimeUnix uint64
	Mode      uint32
	Flags     uint32
	Meta      []byte
}
