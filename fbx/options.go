package fbx

import "runtime"

type Codec uint8

const (
	CodecStore Codec = 0
	CodecZstd  Codec = 1
	CodecLZ4   Codec = 2
)

type Options struct {
	ChunkSizeText int
	ChunkSizeBin  int
	DefaultCodec  Codec
	DefaultLevel  int
	MaxWorkers    int
	MaxEntrySize  uint64
	MaxChunkSize  uint32

	// The four options below default to true. Because Go zero-value is false,
	// passing Options{SomeField: x} would unintentionally disable them.
	// Use the No* flags below to explicitly opt out instead.
	//
	// These fields are read by the runtime but ignored by mergeOptions.
	// They must be set via defaultOptions() or via the No* disable flags.
	DetectText               bool
	StoreIfAlreadyCompressed bool
	SyncOnCommit             bool
	StrictVerify             bool

	// Explicit opt-out flags for the four default-true options above.
	// Set NoStrictVerify: true to disable CRC verification on extraction.
	// Set NoSyncOnCommit: true to skip fsync on commit (faster, less durable).
	// Set NoDetectText: true to disable automatic text/binary detection.
	// Set NoStoreIfAlreadyCompressed: true to force re-compression of already-compressed data.
	NoStrictVerify             bool
	NoSyncOnCommit             bool
	NoDetectText               bool
	NoStoreIfAlreadyCompressed bool
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
