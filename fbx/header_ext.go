package fbx

import (
	"encoding/binary"

	"github.com/pixfid/go-fbx/internal/format"
)

const (
	headerReservedLayoutMarkerOffset  = 0
	headerReservedLayoutMinorOffset   = 1
	headerReservedGenerationOffset    = 24
	headerReservedDirIndexOffset      = 32
	headerReservedDirIndexSize        = 40
	headerReservedDirIndexCRC         = 48
	headerReservedRequiredFeaturesLow = 52
	headerLayoutMarkerV1Extension     = 2
	headerLayoutMinorV1Extension      = 0
)

const (
	requiredFeatureJournalSemantics  uint32 = 1 << 0
	requiredFeatureFixedBackup       uint32 = 1 << 1
	requiredFeatureDirectoryIndex    uint32 = 1 << 2
	requiredFeatureLazyDirectoryOpen uint32 = 1 << 3
)

const requiredFeaturesSupported = requiredFeatureJournalSemantics |
	requiredFeatureFixedBackup |
	requiredFeatureDirectoryIndex |
	requiredFeatureLazyDirectoryOpen

type dirIndexPointer struct {
	offset uint64
	size   uint64
	crc32  uint32
}

func headerHasFixedBackupSlot(h format.HeaderV1) bool {
	return h.Reserved[headerReservedLayoutMarkerOffset] == headerLayoutMarkerV1Extension
}

func readHeaderGeneration(h format.HeaderV1) uint64 {
	return binary.LittleEndian.Uint64(h.Reserved[headerReservedGenerationOffset : headerReservedGenerationOffset+8])
}

func writeHeaderGeneration(h *format.HeaderV1, generation uint64) {
	binary.LittleEndian.PutUint64(h.Reserved[headerReservedGenerationOffset:headerReservedGenerationOffset+8], generation)
}

func readHeaderDirIndexPointer(h format.HeaderV1) dirIndexPointer {
	return dirIndexPointer{
		offset: binary.LittleEndian.Uint64(h.Reserved[headerReservedDirIndexOffset : headerReservedDirIndexOffset+8]),
		size:   binary.LittleEndian.Uint64(h.Reserved[headerReservedDirIndexSize : headerReservedDirIndexSize+8]),
		crc32:  binary.LittleEndian.Uint32(h.Reserved[headerReservedDirIndexCRC : headerReservedDirIndexCRC+4]),
	}
}

func writeHeaderDirIndexPointer(h *format.HeaderV1, p dirIndexPointer) {
	binary.LittleEndian.PutUint64(h.Reserved[headerReservedDirIndexOffset:headerReservedDirIndexOffset+8], p.offset)
	binary.LittleEndian.PutUint64(h.Reserved[headerReservedDirIndexSize:headerReservedDirIndexSize+8], p.size)
	binary.LittleEndian.PutUint32(h.Reserved[headerReservedDirIndexCRC:headerReservedDirIndexCRC+4], p.crc32)
}

func readHeaderRequiredFeaturesLow(h format.HeaderV1) uint32 {
	return binary.LittleEndian.Uint32(h.Reserved[headerReservedRequiredFeaturesLow : headerReservedRequiredFeaturesLow+4])
}

func writeHeaderRequiredFeaturesLow(h *format.HeaderV1, bits uint32) {
	binary.LittleEndian.PutUint32(h.Reserved[headerReservedRequiredFeaturesLow:headerReservedRequiredFeaturesLow+4], bits)
}

func markHeaderV1ExtensionLayout(h *format.HeaderV1) {
	h.Reserved[headerReservedLayoutMarkerOffset] = headerLayoutMarkerV1Extension
	h.Reserved[headerReservedLayoutMinorOffset] = headerLayoutMinorV1Extension
}

func validateCanonicalHeaderLayout(h format.HeaderV1) error {
	requiredFlags := format.HeaderFlagHasJournal |
		format.HeaderFlagHasBackup |
		format.HeaderFlagHasDirIndex |
		format.HeaderFlagHasRequiredFeatures
	if h.Flags&requiredFlags != requiredFlags {
		return ErrInvalidFormat
	}
	if h.Reserved[headerReservedLayoutMarkerOffset] != headerLayoutMarkerV1Extension {
		return ErrInvalidFormat
	}
	if h.Reserved[headerReservedLayoutMinorOffset] != headerLayoutMinorV1Extension {
		return ErrInvalidFormat
	}
	if h.JournalOffset == 0 || h.JournalSize != journalRecordSize {
		return ErrInvalidFormat
	}
	ptr := readHeaderDirIndexPointer(h)
	if ptr.offset == 0 || ptr.size == 0 || ptr.crc32 == 0 {
		return ErrInvalidFormat
	}
	required := readHeaderRequiredFeaturesLow(h)
	if required&requiredFeaturesSupported != requiredFeaturesSupported {
		return ErrInvalidFormat
	}
	return validateHeaderRequiredFeatures(h)
}

func validateHeaderRequiredFeatures(h format.HeaderV1) error {
	if h.Flags&format.HeaderFlagHasRequiredFeatures == 0 {
		return nil
	}
	required := readHeaderRequiredFeaturesLow(h)
	if required&^requiredFeaturesSupported != 0 {
		return ErrUnsupportedFeature
	}
	if required&requiredFeatureJournalSemantics != 0 && h.Flags&format.HeaderFlagHasJournal == 0 {
		return ErrInvalidFormat
	}
	if required&requiredFeatureFixedBackup != 0 && h.Flags&format.HeaderFlagHasBackup == 0 {
		return ErrInvalidFormat
	}
	if required&requiredFeatureDirectoryIndex != 0 && h.Flags&format.HeaderFlagHasDirIndex == 0 {
		return ErrInvalidFormat
	}
	return nil
}
