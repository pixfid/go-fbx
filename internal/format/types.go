package format

type Codec uint8

const (
	CodecStore Codec = 0
	CodecZstd  Codec = 1
	CodecLZ4   Codec = 2
)

type ChunkRefV1 struct {
	ChunkOffset uint64
	RawOffset   uint64
	RawSize     uint32
	CompSize    uint32
	CRC32Raw    uint32
	Reserved    uint32
}

type EntryV1 struct {
	PathHash64 uint64
	MTimeUnix  uint64
	Mode       uint32
	EntryFlags uint32
	FileSize   uint64
	ChunkCount uint32
	MetaSize   uint32
	PathSize   uint32
	Chunks     []ChunkRefV1
	Meta       []byte
	Path       string
}

type DirectoryV1 struct {
	EntryCount uint32
	Flags      uint32
	BuildUnix  uint64
	Entries    []EntryV1
}
